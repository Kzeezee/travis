package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

var pinnedRef = PackageRef{Registry: RegistryNPM, Name: "@scope/server", Version: "1.2.3"}

func buildPinnedServerConfig() config.MCPServerCfg {
	command, args := pinnedRef.BuildLaunch()
	return config.MCPServerCfg{Command: command, Args: args}
}

// writeNPXInstall stands in for what npx leaves in the scratch npm cache after installing pinnedRef.
func writeNPXInstall(t *testing.T, scratchDir, rootIntegrity, depIntegrity string) {
	t.Helper()
	dir := filepath.Join(scratchDir, "npm-cache", "_npx", "9dfaefb6dc6ac94c")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lock := `{"lockfileVersion": 3, "packages": {
		"": {"dependencies": {"@scope/server": "^1.2.3"}},
		"node_modules/@scope/server": {"version": "1.2.3", "integrity": "` + rootIntegrity + `"},
		"node_modules/zod": {"version": "3.0.0", "integrity": "` + depIntegrity + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyInstallLetsALocalCommandRunUnpinned(t *testing.T) {
	state, err := VerifyInstall(t.TempDir(), "notes", config.MCPServerCfg{Command: "/usr/local/bin/notes-server"})
	if err != nil || state != InstallLocal {
		t.Fatalf("got %q, %v; want a local command to need no pin", state, err)
	}
}

func TestVerifyInstallRefusesARegistryServerMCPAddNeverPinned(t *testing.T) {
	_, err := VerifyInstall(t.TempDir(), "github", buildPinnedServerConfig())
	if !errors.Is(err, core.ErrDenied) || !strings.Contains(err.Error(), "travis mcp add") {
		t.Fatalf("got %v; want a denial pointing at travis mcp add", err)
	}
}

func TestVerifyInstallRefusesAConfigEditedToAnotherVersion(t *testing.T) {
	stateDir := t.TempDir()
	if err := RecordInstall(stateDir, "github", pinnedRef, "sha512-root"); err != nil {
		t.Fatal(err)
	}
	cfg := config.MCPServerCfg{Command: "npx", Args: []string{"-y", "@scope/server@1.2.4"}}
	if _, err := VerifyInstall(stateDir, "github", cfg); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("got %v; want a version the user never approved denied", err)
	}
}

func TestVerifyInstallRecordsTheFirstLaunchThenHoldsItsPackagesFixed(t *testing.T) {
	stateDir := t.TempDir()
	scratchDir := buildScratchDir(stateDir, "github")
	if err := RecordInstall(stateDir, "github", pinnedRef, "sha512-root"); err != nil {
		t.Fatal(err)
	}
	if state, err := VerifyInstall(stateDir, "github", buildPinnedServerConfig()); err != nil || state != InstallAwaitingFirstLaunch {
		t.Fatalf("before any launch got %q, %v", state, err)
	}

	writeNPXInstall(t, scratchDir, "sha512-root", "sha512-zod")
	if state, err := VerifyInstall(stateDir, "github", buildPinnedServerConfig()); err != nil || state != InstallVerified {
		t.Fatalf("first launch got %q, %v; want it recorded", state, err)
	}

	writeNPXInstall(t, scratchDir, "sha512-root", "sha512-evil")
	_, err := VerifyInstall(stateDir, "github", buildPinnedServerConfig())
	if !errors.Is(err, core.ErrDenied) || !strings.Contains(err.Error(), "node_modules/zod") {
		t.Fatalf("got %v; want the changed dependency named and denied", err)
	}
}

func TestVerifyInstallRefusesARootPackageThatIsNotTheOneApproved(t *testing.T) {
	stateDir := t.TempDir()
	if err := RecordInstall(stateDir, "github", pinnedRef, "sha512-root"); err != nil {
		t.Fatal(err)
	}
	writeNPXInstall(t, buildScratchDir(stateDir, "github"), "sha512-other", "sha512-zod")
	if _, err := VerifyInstall(stateDir, "github", buildPinnedServerConfig()); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("got %v; want a root integrity that differs from the registry's denied", err)
	}
}

func TestRecordInstallAgainCannotLaunderATamperedInstall(t *testing.T) {
	stateDir := t.TempDir()
	scratchDir := buildScratchDir(stateDir, "github")
	if err := RecordInstall(stateDir, "github", pinnedRef, "sha512-root"); err != nil {
		t.Fatal(err)
	}
	writeNPXInstall(t, scratchDir, "sha512-root", "sha512-zod")
	if _, err := VerifyInstall(stateDir, "github", buildPinnedServerConfig()); err != nil {
		t.Fatal(err)
	}

	writeNPXInstall(t, scratchDir, "sha512-root", "sha512-evil")
	if err := RecordInstall(stateDir, "github", pinnedRef, "sha512-root"); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyInstall(stateDir, "github", buildPinnedServerConfig()); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("got %v; want re-adding the same version to keep what was resolved", err)
	}
}

func TestForgetInstallUnpinsTheServer(t *testing.T) {
	stateDir := t.TempDir()
	if err := RecordInstall(stateDir, "github", pinnedRef, "sha512-root"); err != nil {
		t.Fatal(err)
	}
	if err := ForgetInstall(stateDir, "github"); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyInstall(stateDir, "github", buildPinnedServerConfig()); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("got %v; want a forgotten server refused", err)
	}
	if err := ForgetInstall(t.TempDir(), "never-added"); err != nil {
		t.Fatalf("forgetting with no mcp.lock: %v", err)
	}
}

func TestRecordInstallRefusesAnNPMPackageWithoutAnIntegrityHash(t *testing.T) {
	if err := RecordInstall(t.TempDir(), "github", pinnedRef, ""); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("got %v; want an unpinnable npm package refused", err)
	}
}

func TestVerifyInstallFailsClosedOnACorruptLock(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, lockFileName), []byte("[server\nbroken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyInstall(stateDir, "github", buildPinnedServerConfig()); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("got %v; want a corrupt mcp.lock to refuse registry launches", err)
	}
}

func TestRecordInstallRefusesAPlantedLockSymlink(t *testing.T) {
	stateDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink(target, filepath.Join(stateDir, lockFileName)); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := RecordInstall(stateDir, "github", pinnedRef, "sha512-root"); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("got %v; want the planted link refused", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the write followed the link")
	}
}

// TestRedTeamGateTamperedMCPInstallIsDeniedBeforeAnyLaunch: a dependency swapped in the npx cache never runs,
// and the human is not even asked, since no answer could make the install the approved one.
func TestRedTeamGateTamperedMCPInstallIsDeniedBeforeAnyLaunch(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	process, _ := buildTestProcess(t, buildPinnedServerConfig(), grantedExecPolicy(), approver)
	stateDir := filepath.Dir(process.lockPath)
	if err := RecordInstall(stateDir, process.name, pinnedRef, "sha512-root"); err != nil {
		t.Fatal(err)
	}
	writeNPXInstall(t, process.scratchDir, "sha512-root", "sha512-zod")
	if _, err := verifyInstall(process.lockPath, process.scratchDir, process.name, process.cfg); err != nil {
		t.Fatal(err)
	}
	writeNPXInstall(t, process.scratchDir, "sha512-root", "sha512-evil")

	if _, err := process.connect(t.Context()); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("connect() = %v; want the tampered install denied", err)
	}
	if len(approver.askedOps()) != 0 {
		t.Fatal("a tampered install must be denied without asking")
	}
	if _, err := os.Stat(filepath.Join(process.scratchDir, stderrLogName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the server was launched")
	}
}
