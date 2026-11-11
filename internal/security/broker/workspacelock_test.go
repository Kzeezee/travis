package broker_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Kzeezee/travis/internal/security/broker"
)

// TestClaimWorkspaceRefusesPlantedSymlinks: a cloned repo or compromised process could plant a
// symlink aimed at a file outside the workspace, hoping the session-lock write lands there
// instead (R-26), the same attack AddDecisionRule is hardened against.
func TestClaimWorkspaceRefusesPlantedSymlinks(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, root, outside string){
		".travis is a link": func(t *testing.T, root, outside string) {
			createSymlinkOrSkip(t, outside, filepath.Join(root, ".travis"))
		},
		"session.lock is a link": func(t *testing.T, root, outside string) {
			if err := os.Mkdir(filepath.Join(root, ".travis"), 0o700); err != nil {
				t.Fatal(err)
			}
			createSymlinkOrSkip(t, filepath.Join(outside, "session.lock"), filepath.Join(root, ".travis", "session.lock"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			planted := writeWorkspaceFile(t, outside, "session.lock", "export PATH=$PATH\n")
			plant(t, root, outside)

			if warning := broker.ClaimWorkspace(root, 111); warning == "" {
				t.Fatal("ClaimWorkspace through a planted link: want a warning naming the problem, got none")
			}
			if content, err := os.ReadFile(planted); err != nil || string(content) != "export PATH=$PATH\n" {
				t.Fatalf("the link target now holds %q (%v), want it untouched", content, err)
			}
		})
	}
}
