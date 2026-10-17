package sandbox_test

import (
	"testing"

	llsyscall "github.com/landlock-lsm/go-landlock/landlock/syscall"

	"github.com/Kzeezee/travis/internal/security/sandbox"
)

func TestSandboxReportsTheTierTheKernelSupports(t *testing.T) {
	abi, err := llsyscall.LandlockGetABIVersion()
	want := sandbox.TierLandlock
	switch {
	case err != nil || abi < 1:
		want = sandbox.TierNone
	case abi < 9:
		want = sandbox.TierLandlockNoNet
	}
	_, tier, _ := sandbox.BuildCommand(t.Context(), buildValidSpec(t))
	if tier != want {
		t.Fatalf("landlock abi %d: BuildCommand tier = %q, want %q", abi, tier, want)
	}
}
