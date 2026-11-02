package voicein

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

func writeModuleFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if runtime.GOOS == "windows" && name != "whistle.cact" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveModuleFilesNamesEveryMissingPart(t *testing.T) {
	dir := t.TempDir()
	writeModuleFiles(t, dir, "travis-audio")

	_, err := resolveModuleFilesIn(dir)

	if !errors.Is(err, core.ErrNotFound) || strings.Contains(err.Error(), "travis-audio") ||
		!strings.Contains(err.Error(), "needle") || !strings.Contains(err.Error(), "whistle.cact") {
		t.Fatalf("err = %v, want ErrNotFound naming needle and whistle.cact only", err)
	}
}

func TestResolveModuleFilesFindsAFullModule(t *testing.T) {
	dir := t.TempDir()
	writeModuleFiles(t, dir, "travis-audio", "needle", "whistle.cact")

	files, err := resolveModuleFilesIn(dir)

	if err != nil || files.SpeechModel != filepath.Join(dir, "whistle.cact") || filepath.Dir(files.SpeechEngine) != dir {
		t.Fatalf("files = %+v, err = %v", files, err)
	}
}
