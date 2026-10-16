//go:build !windows

package sandbox_test

import (
	"errors"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/sandbox"
)

// Until an OS has a backend, no target can start sandboxed, so nothing runs unconfined by mistake (§8.1).

func TestStubbedSandboxRefusesToBuildACommand(t *testing.T) {
	cmd, tier, err := sandbox.BuildCommand(t.Context(), buildValidSpec(t))
	if cmd != nil || tier != sandbox.TierNone || !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("BuildCommand = %v, %q, %v; want nil, %q, ErrUnsupported", cmd, tier, err, sandbox.TierNone)
	}
}

func TestStubbedLauncherRefusesToConfine(t *testing.T) {
	if err := sandbox.RunLauncher([]string{encodeSpec(t, buildValidSpec(t))}); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("RunLauncher error = %v, want ErrUnsupported", err)
	}
}
