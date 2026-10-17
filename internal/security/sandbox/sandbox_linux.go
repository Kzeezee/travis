package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"syscall"

	"github.com/landlock-lsm/go-landlock/landlock"
	llsyscall "github.com/landlock-lsm/go-landlock/landlock/syscall"

	"github.com/Kzeezee/travis/internal/core"
)

// Linux backend: the launcher restricts itself with Landlock, which every descendant inherits, then execs the target.

// landlockNetworkABI is the first Landlock ABI that denies Unix socket connects as well as TCP; earlier ones
// leave docker.sock and ssh-agent reachable, so they do not count as network containment.
const landlockNetworkABI = 9

// systemReadPaths are what a dynamically linked target needs to start; missing ones are skipped.
var systemReadPaths = []string{
	"/bin", "/lib", "/lib64", "/usr/bin", "/usr/lib", "/usr/lib64", "/usr/share",
	"/etc/ld.so.cache", "/etc/localtime",
	// OpenSSL reads its config at startup whether or not the process ever connects; node exits without it.
	"/etc/ssl/openssl.cnf",
}

// networkReadPaths are the resolver and trust files that DNS and TLS read.
var networkReadPaths = []string{
	"/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/gai.conf", "/etc/ssl", "/etc/ca-certificates",
}

var deviceWritePaths = []string{"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom"}

func prepareCommandForOS(cmd *exec.Cmd, _ Spec) (Tier, error) {
	tier, err := reportLandlockTier()
	if err != nil {
		return TierNone, err
	}
	killGroupOnCancel(cmd)
	return tier, nil
}

// reportLandlockTier fails closed without Landlock, since there is nothing else to fall back to (OQ-16).
func reportLandlockTier() (Tier, error) {
	abi, err := llsyscall.LandlockGetABIVersion()
	switch {
	case err != nil:
		return TierNone, fmt.Errorf("landlock unavailable: %w: %w", err, core.ErrUnsupported)
	case abi < 1:
		return TierNone, fmt.Errorf("landlock unavailable: %w", core.ErrUnsupported)
	case abi < landlockNetworkABI:
		return TierLandlockNoNet, nil
	default:
		return TierLandlock, nil
	}
}

// applyToSelf uses best effort so older kernels keep what they support; the parent already reported that tier.
// Signal and abstract Unix socket scoping stay on with AllowNetwork, which grants IP only.
func applyToSelf(spec Spec) error {
	if _, err := reportLandlockTier(); err != nil {
		return err
	}
	config := landlock.V10.BestEffort()
	readPaths := slices.Concat(systemReadPaths, spec.ReadPaths)
	if spec.AllowNetwork {
		config.HandledAccessNet = 0
		readPaths = append(readPaths, networkReadPaths...)
	}
	var rules []landlock.Rule
	for _, path := range readPaths {
		rules = append(rules, buildPathRule(path, false).IgnoreIfMissing())
	}
	for _, path := range deviceWritePaths {
		rules = append(rules, buildPathRule(path, true).IgnoreIfMissing())
	}
	for _, path := range spec.WritePaths {
		rules = append(rules, buildPathRule(path, true))
	}
	err := config.Restrict(rules...)
	if err != nil {
		return fmt.Errorf("apply landlock: %w", err)
	}
	return nil
}

// buildPathRule grants file-only rights to a non-directory, because Landlock rejects directory rights on one.
func buildPathRule(path string, canWrite bool) landlock.FSRule {
	info, err := os.Stat(path)
	isDir := err != nil || info.IsDir()
	switch {
	case canWrite && isDir:
		// Refer only moves files between places already writable; without it a rename across two folders is EXDEV.
		return landlock.RWDirs(path).WithRefer()
	case canWrite:
		return landlock.RWFiles(path)
	case isDir:
		return landlock.RODirs(path)
	default:
		return landlock.ROFiles(path)
	}
}

// execTarget replaces the launcher with the target, so it keeps the launcher's pid, group, stdio and Landlock domain.
func execTarget(spec Spec) error {
	if err := os.Chdir(spec.Dir); err != nil {
		return fmt.Errorf("enter sandbox dir: %w", err)
	}
	path, err := exec.LookPath(spec.Argv[0])
	if err != nil {
		return fmt.Errorf("find sandboxed target: %w", err)
	}
	if err := syscall.Exec(path, spec.Argv, spec.Env); err != nil {
		return fmt.Errorf("exec sandboxed target: %w", err)
	}
	return nil
}
