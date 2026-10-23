package sandbox_test

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/Kzeezee/travis/internal/security/sandbox"
)

// A runner under Program Files sits in a folder Users can read but the user cannot re-permission;
// the launcher must still start the target there instead of failing on the ACL grant.
func TestSandboxReadsFolderUserCannotRepermission(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	locked := t.TempDir()
	runner := filepath.Join(locked, "runner.txt")
	if err := os.WriteFile(runner, []byte("runner"), 0o600); err != nil {
		t.Fatal(err)
	}
	// OWNER RIGHTS drops the owner's implicit WRITE_DAC; delete rights let t.TempDir clean up.
	lockFolderPermissions(t, locked, "D:P(A;OICI;FRFXSDDT;;;OW)(A;OICI;FRFX;;;BU)")

	spec := buildProbeSpec(t, self, "read", runner)
	spec.ReadPaths = append(spec.ReadPaths, locked)
	assertProbeAllowed(t, spec)
}

func lockFolderPermissions(t *testing.T, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func assertProbeAllowed(t *testing.T, spec sandbox.Spec) {
	t.Helper()
	cmd, tier, err := sandbox.BuildCommand(t.Context(), spec)
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tier %s did not let the probe read its granted folder (%v):\n%s", tier, err, output)
	}
}
