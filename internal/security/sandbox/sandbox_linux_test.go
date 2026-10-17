package sandbox_test

import (
	"os"
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

// Landlock v4 to v8 still deny TCP even though their tier does not claim network containment, so the escape test skips it.
func TestLandlockDeniesTCPWithoutAllowNetwork(t *testing.T) {
	if abi, err := llsyscall.LandlockGetABIVersion(); err != nil || abi < 4 {
		t.Skipf("landlock abi %d cannot deny TCP: %v", abi, err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	assertProbeDenied(t, buildProbeSpec(t, self, "connect", listenOnLoopbackTCP(t)))
}

// Node's OpenSSL reads openssl.cnf at startup even offline, so it is readable without AllowNetwork; the rest of /etc/ssl is not (TRA-61).
func TestLandlockGrantsOpenSSLConfigWithoutNetwork(t *testing.T) {
	const opensslConfig, certBundle = "/etc/ssl/openssl.cnf", "/etc/ssl/certs/ca-certificates.crt"
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(opensslConfig); err != nil {
		t.Skipf("no %s on this host: %v", opensslConfig, err)
	}
	assertProbeEscaped(t, buildProbeSpec(t, self, "read", opensslConfig))
	if _, err := os.Stat(certBundle); err != nil {
		t.Skipf("no %s on this host: %v", certBundle, err)
	}
	assertProbeDenied(t, buildProbeSpec(t, self, "read", certBundle))
}
