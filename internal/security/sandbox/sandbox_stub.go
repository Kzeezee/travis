package sandbox

import (
	"fmt"
	"os/exec"

	"github.com/Kzeezee/travis/internal/core"
)

// No OS has a backend yet, so every sandboxed launch fails closed (§8.1); 4B to 4D narrow this file's build tag.

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
