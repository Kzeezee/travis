package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Kzeezee/travis/internal/core"
)

// macOS backend: the launcher execs the target through sandbox-exec with a generated deny-by-default profile.
// Paths reach the profile as -D parameters, so no path text is ever spliced into the profile itself.

const sandboxExecPath = "/usr/bin/sandbox-exec"

// baseSeatbeltProfile is what any process needs to start: dyld reads "/" itself and the system libraries,
// and metadata is readable everywhere so path resolution works; file contents stay denied outside the allowlist.
const baseSeatbeltProfile = `(version 1)
(deny default)
(allow process-fork)
(allow signal (target same-sandbox))
(allow process-info* (target same-sandbox))
(allow sysctl-read)
(allow file-read-metadata)
(allow file-read* (literal "/"))
(allow file-read* process-exec
  (subpath "/System") (subpath "/usr/lib") (subpath "/usr/bin") (subpath "/bin") (subpath "/usr/share")
  (subpath "/private/var/db/timezone") (literal "/private/etc/localtime"))
(allow file-read* file-write-data
  (literal "/dev/null") (literal "/dev/zero") (literal "/dev/random") (literal "/dev/urandom"))
`

// networkSeatbeltProfile adds IP sockets plus the resolver and trust services that DNS and TLS go through.
// Unix sockets stay denied except mDNSResponder, which every hostname lookup goes through: an unfiltered
// network* would reach local services such as docker.sock or ssh-agent.
const networkSeatbeltProfile = `(allow system-socket)
(allow network-outbound (remote ip) (literal "/private/var/run/mDNSResponder"))
(allow network-bind network-inbound (local ip))
(allow mach-lookup
  (global-name "com.apple.dnssd.service") (global-name "com.apple.trustd") (global-name "com.apple.trustd.agent")
  (global-name "com.apple.SecurityServer"))
(allow file-read* (subpath "/private/etc") (literal "/private/var/run/resolv.conf"))
`

func prepareCommandForOS(cmd *exec.Cmd, _ Spec) (Tier, error) {
	if _, err := os.Stat(sandboxExecPath); err != nil {
		return TierNone, fmt.Errorf("find %s: %w", sandboxExecPath, core.ErrUnsupported)
	}
	killGroupOnCancel(cmd)
	return TierSeatbelt, nil
}

// applyToSelf has nothing to do: sandbox-exec applies the profile as it execs the target, never to the launcher.
func applyToSelf(Spec) error {
	return nil
}

// execTarget replaces the launcher with sandbox-exec, so the target keeps the launcher's pid, group and stdio.
func execTarget(spec Spec) error {
	if err := os.Chdir(spec.Dir); err != nil {
		return fmt.Errorf("enter sandbox dir: %w", err)
	}
	args := buildSandboxExecArgs(spec)
	if err := syscall.Exec(sandboxExecPath, args, spec.Env); err != nil {
		return fmt.Errorf("exec %s: %w", sandboxExecPath, err)
	}
	return nil
}

func buildSandboxExecArgs(spec Spec) []string {
	args := []string{sandboxExecPath}
	var profile strings.Builder
	profile.WriteString(baseSeatbeltProfile)
	for i, path := range spec.ReadPaths {
		param := "READ_" + strconv.Itoa(i)
		args = append(args, "-D", param+"="+resolveSeatbeltPath(path))
		fmt.Fprintf(&profile, "(allow file-read* process-exec (subpath (param %q)))\n", param)
	}
	for i, path := range spec.WritePaths {
		param := "WRITE_" + strconv.Itoa(i)
		args = append(args, "-D", param+"="+resolveSeatbeltPath(path))
		fmt.Fprintf(&profile, "(allow file-read* file-write* process-exec (subpath (param %q)))\n", param)
	}
	if spec.AllowNetwork {
		profile.WriteString(networkSeatbeltProfile)
	}
	args = append(args, "-p", profile.String(), "--")
	return append(args, spec.Argv...)
}

// resolveSeatbeltPath follows symlinks because Seatbelt matches real paths: /var and /tmp live under /private.
func resolveSeatbeltPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return resolved
}
