//go:build !windows && !darwin

package sandbox

import (
	"fmt"
	"os/exec"

	"github.com/Kzeezee/travis/internal/core"
)

// OSes without a backend fail every sandboxed launch closed (§8.1); 4B narrows this file's build tag further.

var errSandboxUnsupportedOS = fmt.Errorf("no sandbox backend for this OS: %w", core.ErrUnsupported)

func prepareCommandForOS(*exec.Cmd, Spec) (Tier, error) {
	return TierNone, errSandboxUnsupportedOS
}

func applyToSelf(Spec) error {
	return errSandboxUnsupportedOS
}

func execTarget(Spec) error {
	return errSandboxUnsupportedOS
}
