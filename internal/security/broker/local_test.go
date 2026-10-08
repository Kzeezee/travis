package broker_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

func writeWorkspaceFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func createSymlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
}

func TestLocalBrokerDescribesPathsRelativeToTheWorkspace(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "notes/today.md", "hi")
	local := broker.BuildLocalBroker(root)

	file, err := local.DescribePath(context.Background(), "notes/today.md")
	if err != nil {
		t.Fatal(err)
	}
	if file.Kind != core.PathKindFile || file.WorkspacePath != "notes/today.md" || !filepath.IsAbs(file.Path) {
		t.Fatalf("DescribePath(file) = %+v", file)
	}

	dir, err := local.DescribePath(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	if dir.Kind != core.PathKindDirectory || dir.WorkspacePath != "." {
		t.Fatalf("DescribePath(root) = %+v", dir)
	}

	again, err := local.DescribePath(context.Background(), file.Path)
	if err != nil || again != file {
		t.Fatalf("DescribePath(reported path) = %+v, %v; want %+v", again, err, file)
	}
}

func TestLocalBrokerDeniesPathsOutsideTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	writeWorkspaceFile(t, root, "inside.txt", "in")
	outside := writeWorkspaceFile(t, parent, "outside.txt", "out")
	local := broker.BuildLocalBroker(root)

	for _, path := range []string{"../outside.txt", "../missing.txt", outside} {
		t.Run(path, func(t *testing.T) {
			if _, err := local.DescribePath(context.Background(), path); !errors.Is(err, core.ErrDenied) {
				t.Fatalf("DescribePath(%q) error = %v, want ErrDenied", path, err)
			}
			if _, err := local.OpenFile(context.Background(), path); !errors.Is(err, core.ErrDenied) {
				t.Fatalf("OpenFile(%q) error = %v, want ErrDenied", path, err)
			}
		})
	}
}

func TestLocalBrokerDeniesSymlinksThatEscapeTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	writeWorkspaceFile(t, root, "inside.txt", "in")
	outside := writeWorkspaceFile(t, parent, "outside.txt", "out")
	createSymlinkOrSkip(t, outside, filepath.Join(root, "escape.txt"))
	local := broker.BuildLocalBroker(root)

	if _, err := local.OpenFile(context.Background(), "escape.txt"); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("OpenFile(escape.txt) error = %v, want ErrDenied", err)
	}
}

func TestLocalBrokerReportsMissingPathsAndDirectories(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "folder/file.txt", "x")
	local := broker.BuildLocalBroker(root)

	if _, err := local.OpenFile(context.Background(), "missing.txt"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("OpenFile(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := local.OpenFile(context.Background(), "folder"); !errors.Is(err, core.ErrIsDirectory) {
		t.Fatalf("OpenFile(folder) error = %v, want ErrIsDirectory", err)
	}
	if _, err := local.ListDirectory(context.Background(), "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("ListDirectory(missing) error = %v, want ErrNotFound", err)
	}
}

func TestLocalBrokerOpensFilesWithTheirResolvedInfo(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "a.txt", "contents")
	local := broker.BuildLocalBroker(root)

	opened, err := local.OpenFile(context.Background(), "workspace:a.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Content.Close()
	data, err := io.ReadAll(opened.Content)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "contents" || opened.Info.WorkspacePath != "a.txt" {
		t.Fatalf("OpenFile = %q, %+v", data, opened.Info)
	}
}

func TestLocalBrokerListsEntriesInNameOrderWithoutFollowingSymlinks(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "b.txt", "b")
	writeWorkspaceFile(t, root, "a/inner.txt", "a")
	createSymlinkOrSkip(t, root, filepath.Join(root, "c-loop"))
	local := broker.BuildLocalBroker(root)

	entries, err := local.ListDirectory(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		workspacePath string
		kind          core.PathKind
	}{{"a", core.PathKindDirectory}, {"b.txt", core.PathKindFile}, {"c-loop", core.PathKindSymlink}}
	if len(entries) != len(want) {
		t.Fatalf("ListDirectory = %+v, want %d entries", entries, len(want))
	}
	for i, entry := range entries {
		if entry.WorkspacePath != want[i].workspacePath || entry.Kind != want[i].kind {
			t.Errorf("entry %d = %+v, want %s (%s)", i, entry, want[i].workspacePath, want[i].kind)
		}
	}
}

func TestLocalBrokerRejectsUnknownPathPrefixesAsInvalid(t *testing.T) {
	local := broker.BuildLocalBroker(t.TempDir())
	if _, err := local.DescribePath(context.Background(), "secrets:token"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("DescribePath(secrets:token) error = %v, want ErrInvalid", err)
	}
}

func TestLocalBrokerStopsWhenTheContextIsCancelled(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "a.txt", "a")
	local := broker.BuildLocalBroker(root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := local.OpenFile(ctx, "a.txt"); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenFile error = %v, want context.Canceled", err)
	}
	if _, err := local.ListDirectory(ctx, "."); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListDirectory error = %v, want context.Canceled", err)
	}
}
