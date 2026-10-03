package builtin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/tools/builtin"
	"github.com/Kzeezee/travis/internal/tools/tooltest"
)

func invokeGrep(t *testing.T, tool *builtin.GrepTool, arguments string) core.ToolOutput {
	t.Helper()
	output, err := tool.Invoke(context.Background(), json.RawMessage(arguments))
	if err != nil {
		t.Fatalf("Invoke(%s) returned a Go error: %v", arguments, err)
	}
	return output
}

func writeNotesFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "inbox.md", "buy milk\nTODO: call the plumber\n")
	writeFixture(t, root, "notes/2026-09-01-standup.md", "# Standup\nTODO ship grep\ndone: config\nTODO review trace\n")
	writeFixture(t, root, "notes/projects/garden.md", "Garden plan\nno open items\n")
	writeFixture(t, root, "notes/projects/travis.txt", "TODO pick a logo\n")
	return root
}

func TestGrepReturnsMatchingLinesWithPathsAndLineNumbers(t *testing.T) {
	tool := builtin.BuildGrepTool(writeNotesFixture(t))

	output := invokeGrep(t, tool, `{"pattern":"TODO"}`)

	assertContent(t, output, "inbox.md:2:TODO: call the plumber\n"+
		"notes/2026-09-01-standup.md:2:TODO ship grep\n"+
		"notes/2026-09-01-standup.md:4:TODO review trace\n"+
		"notes/projects/travis.txt:1:TODO pick a logo")
	if output.Display != `"TODO": 4 matches in 3 files` {
		t.Fatalf("Display = %q", output.Display)
	}
}

func TestGrepListsEveryFileWhenFilesModeHasNoPattern(t *testing.T) {
	tool := builtin.BuildGrepTool(writeNotesFixture(t))

	output := invokeGrep(t, tool, `{"mode":"files"}`)

	assertContent(t, output, "inbox.md\nnotes/2026-09-01-standup.md\nnotes/projects/garden.md\nnotes/projects/travis.txt")
	if output.Display != "files: 4 files" {
		t.Fatalf("Display = %q", output.Display)
	}
}

func TestGrepListsOnlyFilesContainingThePatternOnceEach(t *testing.T) {
	tool := builtin.BuildGrepTool(writeNotesFixture(t))

	assertContent(t, invokeGrep(t, tool, `{"pattern":"TODO","mode":"files"}`),
		"inbox.md\nnotes/2026-09-01-standup.md\nnotes/projects/travis.txt")
}

func TestGrepFiltersByIncludeGlob(t *testing.T) {
	tool := builtin.BuildGrepTool(writeNotesFixture(t))

	for _, tc := range []struct {
		name, arguments, want string
	}{
		{"slash-free glob matches at any depth", `{"mode":"files","include":"*.md"}`,
			"inbox.md\nnotes/2026-09-01-standup.md\nnotes/projects/garden.md"},
		{"double star glob", `{"mode":"files","include":"notes/**/*.txt"}`, "notes/projects/travis.txt"},
		{"brace set", `{"mode":"files","include":"**/*.{txt,md}","path":"notes/projects"}`,
			"notes/projects/garden.md\nnotes/projects/travis.txt"},
		{"glob is relative to path", `{"pattern":"TODO","path":"notes","include":"projects/*"}`,
			"notes/projects/travis.txt:1:TODO pick a logo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertContent(t, invokeGrep(t, tool, tc.arguments), tc.want)
		})
	}
}

func TestGrepSearchesASingleFileWhenPathIsAFile(t *testing.T) {
	tool := builtin.BuildGrepTool(writeNotesFixture(t))

	output := invokeGrep(t, tool, `{"pattern":"(?i)todo","path":"inbox.md","include":"*.md"}`)

	assertContent(t, output, "inbox.md:2:TODO: call the plumber")
	if output.Display != `"(?i)todo" in inbox.md (*.md): 1 match in 1 file` {
		t.Fatalf("Display = %q", output.Display)
	}
}

func TestGrepReportsNoMatchesAsAResultNotAnError(t *testing.T) {
	tool := builtin.BuildGrepTool(writeNotesFixture(t))

	assertContent(t, invokeGrep(t, tool, `{"pattern":"nothing-here"}`), "[no matches]")
	assertContent(t, invokeGrep(t, tool, `{"mode":"files","include":"*.go"}`), "[no files found]")
}

func TestGrepSkipsHiddenEntriesUnlessSearchedDirectly(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".git/config", "needle")
	writeFixture(t, root, ".env", "needle")
	writeFixture(t, root, "notes/.draft.md", "needle")
	writeFixture(t, root, "notes/visible.md", "needle")
	tool := builtin.BuildGrepTool(root)

	assertContent(t, invokeGrep(t, tool, `{"pattern":"needle","mode":"files"}`), "notes/visible.md")
	assertContent(t, invokeGrep(t, tool, `{"pattern":"needle","path":".git"}`), ".git/config:1:needle")
}

func TestGrepSkipsBinaryFiles(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "image.bin", "needle\x00\x01")
	writeFixture(t, root, "text.txt", "needle")
	tool := builtin.BuildGrepTool(root)

	assertContent(t, invokeGrep(t, tool, `{"pattern":"needle"}`), "text.txt:1:needle")
}

func TestGrepFollowsSymlinkedFilesOnlyInsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	inside := writeFixture(t, root, "real/inside.txt", "needle")
	outside := writeFixture(t, t.TempDir(), "secret.txt", "needle")
	if err := os.Symlink(outside, filepath.Join(root, "escape.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(inside, filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "real/loop")); err != nil {
		t.Fatal(err)
	}
	tool := builtin.BuildGrepTool(root)

	assertContent(t, invokeGrep(t, tool, `{"pattern":"needle","mode":"files"}`), "alias.txt\nreal/inside.txt")
}

func TestGrepRejectsPathsOutsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	tool := builtin.BuildGrepTool(root)

	assertErrorCode(t, invokeGrep(t, tool, `{"pattern":"x","path":"../"}`), "path_outside_workspace")
	assertErrorCode(t, invokeGrep(t, tool, `{"pattern":"x","path":"missing"}`), "file_not_found")
}

func TestGrepStopsAtTheResultCapAndSaysHowToNarrow(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "many.txt", strings.Repeat("hit\n", 250))
	tool := builtin.BuildGrepTool(root)

	output := invokeGrep(t, tool, `{"pattern":"hit"}`)

	lines := strings.Split(output.Content, "\n")
	if len(lines) != 201 || lines[199] != "many.txt:200:hit" {
		t.Fatalf("got %d lines, line 200 = %q", len(lines), lines[199])
	}
	if !strings.HasPrefix(lines[200], "[stopped after 200 results;") {
		t.Fatalf("missing truncation note: %q", lines[200])
	}
	if output.Display != `"hit": 200+ matches in 1 file` {
		t.Fatalf("Display = %q", output.Display)
	}
}

func TestGrepCountsFilesAgainstTheCapInFilesMode(t *testing.T) {
	root := t.TempDir()
	for i := range 201 {
		writeFixture(t, root, fmt.Sprintf("f%03d.txt", i), "x")
	}
	tool := builtin.BuildGrepTool(root)

	output := invokeGrep(t, tool, `{"mode":"files"}`)

	if !strings.HasSuffix(output.Content, "f199.txt\n[stopped after 200 results; narrow pattern, path or include to see the rest]") {
		t.Fatalf("unexpected tail: %q", output.Content[len(output.Content)-120:])
	}
	if output.Display != "files: 200+ files" {
		t.Fatalf("Display = %q", output.Display)
	}
}

func TestGrepShortensLongLinesAndKeepsCountingPastOversizeOnes(t *testing.T) {
	root := t.TempDir()
	oversize := "needle" + strings.Repeat("x", 100<<10)
	writeFixture(t, root, "long.txt", oversize+"\r\nneedle again\r\n")
	tool := builtin.BuildGrepTool(root)

	output := invokeGrep(t, tool, `{"pattern":"needle"}`)

	assertContent(t, output, "long.txt:1:needle"+strings.Repeat("x", 294)+"…\nlong.txt:2:needle again")
}

func TestGrepRejectsInvalidArguments(t *testing.T) {
	tool := builtin.BuildGrepTool(t.TempDir())

	for _, arguments := range []string{
		`{}`,
		`{"pattern":""}`,
		`{"patern":"x"}`,
		`{"pattern":"x","mode":"lines"}`,
		`{"pattern":"("}`,
		`{"pattern":"x","include":"[a"}`,
		`{"pattern":"x"} {}`,
		`{"pattern":"x","path":"nope:dir"}`,
		`[]`,
		`null`,
	} {
		t.Run(arguments, func(t *testing.T) {
			assertErrorCode(t, invokeGrep(t, tool, arguments), "invalid_arguments")
		})
	}
}

func TestGrepReturnsAGoErrorWhenCancelled(t *testing.T) {
	tool := builtin.BuildGrepTool(writeNotesFixture(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := tool.Invoke(ctx, json.RawMessage(`{"pattern":"TODO"}`)); err == nil {
		t.Fatal("expected a Go error for a cancelled context")
	}
}

func TestGrepToolMeetsTheToolContract(t *testing.T) {
	tooltest.Run(t, builtin.BuildGrepTool(t.TempDir()))
}

func TestGrepDescribeReportsTheReadCapability(t *testing.T) {
	root := t.TempDir()
	spec := builtin.BuildGrepTool(root).Describe()

	if spec.Name != "grep" {
		t.Fatalf("Describe().Name = %q", spec.Name)
	}
	if len(spec.Capabilities) != 1 || spec.Capabilities[0].Kind != core.CapabilityFSRead ||
		spec.Capabilities[0].Scope != filepath.Clean(root) {
		t.Fatalf("Capabilities = %+v", spec.Capabilities)
	}
}
