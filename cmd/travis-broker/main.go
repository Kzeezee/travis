// Command travis-broker is one session's effect broker: travis spawns it as a child in the
// workspace, and it performs file effects only where policy allows (D-12, §4.4). Wiring only.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/id"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/security/sandbox"
	"github.com/Kzeezee/travis/internal/state/blobs"
	"github.com/Kzeezee/travis/internal/state/memory"
	"github.com/Kzeezee/travis/internal/state/session/filestore"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == sandbox.LauncherArg {
		if err := sandbox.RunLauncher(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "travis-broker:", err)
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "travis-broker:", err)
		stop()
		os.Exit(1)
	}
}

// run writes exactly one line to readiness, once it is listening; travis waits for it.
func run(ctx context.Context, args []string, readiness, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("travis-broker", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	endpoint := flags.String("endpoint", "", "socket path or pipe name to listen on, chosen by travis")
	clientPID := flags.Int("client-pid", 0, "process id of the travis process allowed to connect")
	profile := flags.String("profile", "default", "security profile whose grants this session holds")
	sessionID := flags.String("session", "", "id of the session whose log this broker keeps, chosen by travis")
	snapshotMaxBytes := flags.Int64("snapshot-max-bytes", 0, "largest file backed up before a write; larger writes cannot be undone")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *endpoint == "" || *clientPID <= 0 || *sessionID == "" || *snapshotMaxBytes <= 0 {
		return fmt.Errorf("-endpoint, -session, a positive -client-pid and a positive -snapshot-max-bytes are required")
	}

	paths, err := config.BuildCurrentPathSet()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home directory: %w", err)
	}
	sessionPolicy, err := broker.BuildSessionPolicy(*profile, broker.SessionRoots{Paths: paths, Home: home, Temp: os.TempDir()})
	if err != nil {
		return err
	}
	sessionLog, err := filestore.Open(filepath.Join(paths.StateDir, "sessions"), core.ID(*sessionID))
	if err != nil {
		return err
	}
	defer sessionLog.Close()
	backups, err := blobs.Open(filepath.Join(paths.StateDir, "blobs"), *snapshotMaxBytes)
	if err != nil {
		return err
	}
	clock := core.SystemClock{}
	records := broker.SessionRecords{Log: sessionLog, Backups: backups, IDs: id.BuildULIDGenerator(clock)}
	effects := broker.BuildPolicyEnforcedBroker(paths.WorkspaceDir, sessionPolicy, records, memory.BuildStore(paths.StateDir), paths.StateDir, clock)

	sessionListener, err := broker.ListenForSession(broker.SessionEndpoint(*endpoint))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(readiness, broker.BrokerReadyLine); err != nil {
		_ = sessionListener.Close()
		return err
	}
	return sessionListener.ServeOneClient(ctx, *clientPID, effects)
}
