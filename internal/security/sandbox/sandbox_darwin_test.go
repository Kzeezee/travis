package sandbox_test

import (
	"testing"

	"github.com/Kzeezee/travis/internal/security/sandbox"
)

func TestSandboxReportsSeatbeltTier(t *testing.T) {
	_, tier, err := sandbox.BuildCommand(t.Context(), buildValidSpec(t))
	if err != nil || tier != sandbox.TierSeatbelt {
		t.Fatalf("BuildCommand tier = %q, %v; want %q", tier, err, sandbox.TierSeatbelt)
	}
}
