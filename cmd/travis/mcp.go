package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/redact"
	"github.com/Kzeezee/travis/internal/tools/mcp"
)

const (
	mcpUsage = "usage: travis mcp list | add <name> [--allow-net] [--env KEY=VALUE]... [--] <command> [args...] | remove <name>"
	// registryTimeout bounds the provenance lookup; npm's full document for a busy package can be slow.
	registryTimeout = 20 * time.Second
)

// mcpInstall is what `travis mcp add` needs from outside: the registry, the user's answer, and the config file.
type mcpInstall struct {
	in            io.Reader
	out           io.Writer
	isInteractive bool
	lookup        interface {
		FetchProvenance(context.Context, mcp.PackageRef) (mcp.Provenance, error)
	}
	paths config.PathSet
	now   time.Time
}

func runMCP(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(mcpUsage)
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New(mcpUsage)
		}
		return listMCPServers(out)
	case "add", "remove":
		paths, err := config.BuildCurrentPathSet()
		if err != nil {
			return err
		}
		if args[0] == "remove" {
			return removeMCPServer(args[1:], out, paths)
		}
		install := mcpInstall{
			in: in, out: out, isInteractive: isInteractiveInput(in),
			lookup: mcp.BuildRegistryLookup(&http.Client{Timeout: registryTimeout}),
			paths:  paths, now: core.SystemClock{}.ReportCurrentTime(),
		}
		return install.addServer(args[1:])
	default:
		return fmt.Errorf("unknown mcp subcommand %q (%s)", args[0], mcpUsage)
	}
}

// isInteractiveInput is true only for a terminal: an install must be confirmed by a person, never piped in
// by a script or by the model running a command (DESIGN.md §5.7, R-01).
func isInteractiveInput(in io.Reader) bool {
	file, ok := in.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// listMCPServers shows configured servers; whether each one started is only known inside a chat (/mcp).
func listMCPServers(out io.Writer) error {
	cfg, _, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.MCP.Servers) == 0 {
		fmt.Fprintln(out, "no MCP servers configured; add one with travis mcp add")
		return nil
	}
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, name := range slices.Sorted(maps.Keys(cfg.MCP.Servers)) {
		server := cfg.MCP.Servers[name]
		fmt.Fprintf(table, "%s\t%s\n", name, describeLaunch(server))
	}
	return table.Flush()
}

// describeLaunch scrubs known secret formats: args can carry a token, as `config show` assumes of env.
func describeLaunch(server config.MCPServerCfg) string {
	return redact.New().RedactText(strings.Join(append([]string{server.Command}, server.Args...), " "))
}

func removeMCPServer(args []string, out io.Writer, paths config.PathSet) error {
	if len(args) != 1 {
		return errors.New(mcpUsage)
	}
	wasConfigured, err := config.RemoveMCPServer(paths, args[0])
	if err != nil {
		return err
	}
	if !wasConfigured {
		fmt.Fprintf(out, "no MCP server named %q was configured\n", args[0])
		return nil
	}
	fmt.Fprintf(out, "removed MCP server %q\n", args[0])
	return nil
}

// addServer shows the provenance card and saves the server only when the user types yes.
// Adding a name that exists replaces it, which is also how a server is updated to a new version.
func (i mcpInstall) addServer(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New(mcpUsage)
	}
	name := args[0]
	flags := flag.NewFlagSet("mcp add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	allowNet := flags.Bool("allow-net", false, "")
	env := map[string]string{}
	flags.Func("env", "", func(pair string) error {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return fmt.Errorf("--env takes KEY=VALUE, not %q", pair)
		}
		env[key] = value
		return nil
	})
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("%w (%s)", err, mcpUsage)
	}
	launch := flags.Args()
	if len(launch) == 0 {
		return errors.New(mcpUsage)
	}
	server := config.MCPServerCfg{Command: launch[0], Args: launch[1:], AllowNet: *allowNet}
	if len(env) > 0 {
		server.Env = env
	}
	if !i.isInteractive {
		return fmt.Errorf("travis mcp add must be confirmed at a terminal; it never installs from piped input: %w", core.ErrInvalid)
	}

	ref, isRegistry, err := mcp.ResolvePackageRef(server.Command, server.Args)
	if err != nil {
		return err
	}
	card := []string{"install MCP server " + name, "  runs        " + describeLaunch(server)}
	if existing, err := i.findConfiguredServer(name); err == nil && existing != nil {
		card = append(card, "  replaces    "+describeLaunch(*existing))
	}
	if isRegistry {
		ctx, cancel := context.WithTimeout(context.Background(), registryTimeout)
		provenance, err := i.lookup.FetchProvenance(ctx, ref)
		cancel()
		if errors.Is(err, core.ErrInvalid) {
			return err
		}
		card = append(card, i.describeProvenance(ref, provenance, err)...)
		if !server.AllowNet {
			card = append(card, "  ⚠ "+string(ref.Registry)+" packages download on first launch; without --allow-net that launch will fail")
		}
	} else {
		card = append(card, "  package     local command, not from a registry: there is no provenance to check")
	}
	card = append(card, i.describeRequests(server)...)
	card = append(card,
		"  sandbox     runs in the OS sandbox: its own scratch folder only, network only with --allow-net",
		"  ⚠ This runs third-party code on your machine. travis asks again before its first launch.",
	)
	fmt.Fprintln(i.out, strings.Join(card, "\n"))

	fmt.Fprint(i.out, "install? [y/N] ")
	answer, _ := bufio.NewReader(i.in).ReadString('\n')
	if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "y" && answer != "yes" {
		fmt.Fprintln(i.out, "not installed")
		return nil
	}
	if err := config.SaveMCPServer(i.paths, name, server); err != nil {
		return err
	}
	fmt.Fprintf(i.out, "added MCP server %q to %s; it starts with your next travis chat (see /mcp)\n", name, i.paths.ReportUserConfigFile())
	return nil
}

func (i mcpInstall) findConfiguredServer(name string) (*config.MCPServerCfg, error) {
	cfg, _, err := config.Load()
	if err != nil {
		return nil, err
	}
	if server, ok := cfg.MCP.Servers[name]; ok {
		return &server, nil
	}
	return nil, nil
}

// describeProvenance is the card's registry half; an unreachable registry is shown, not hidden.
func (i mcpInstall) describeProvenance(ref mcp.PackageRef, provenance mcp.Provenance, lookupErr error) []string {
	lines := []string{fmt.Sprintf("  package     %s @ %s (%s)", ref.Name, ref.Version, ref.Registry)}
	if lookupErr != nil {
		return append(lines, "  ⚠ registry  could not be checked: "+lookupErr.Error())
	}
	lines = append(lines, "  publisher   "+reportOrUnknown(provenance.Publisher))
	if !provenance.PublishedAt.IsZero() {
		days := int(i.now.Sub(provenance.PublishedAt).Hours() / 24)
		lines = append(lines, fmt.Sprintf("  published   %s (%d days ago)", provenance.PublishedAt.Format(time.DateOnly), days))
	}
	if provenance.WeeklyDownloads >= 0 {
		lines = append(lines, fmt.Sprintf("  downloads   %d last week", provenance.WeeklyDownloads))
	}
	lines = append(lines,
		fmt.Sprintf("  deps        %d direct", provenance.DependencyCount),
		"  integrity   "+reportOrUnknown(provenance.Integrity),
	)
	return lines
}

func (i mcpInstall) describeRequests(server config.MCPServerCfg) []string {
	network := "none"
	if server.AllowNet {
		network = "allowed (--allow-net)"
	}
	lines := []string{"  network     " + network}
	if len(server.Env) > 0 {
		lines = append(lines, "  env         "+strings.Join(slices.Sorted(maps.Keys(server.Env)), ", ")+" (stored in config.toml as plain text)")
	}
	return lines
}

func reportOrUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
