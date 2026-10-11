package builtin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/tools/builtin"
	"github.com/Kzeezee/travis/internal/tools/tooltest"
)

func buildConfinedFileEditTool(root string) *builtin.FileEditTool {
	return builtin.BuildFileEditTool(broker.BuildLocalBroker(root), root, testMaxBytes)
}

func invokeEdit(t *testing.T, tool *builtin.FileEditTool, path, oldString, newString string) core.ToolOutput {
	t.Helper()
	arguments, err := json.Marshal(map[string]string{"path": path, "old_string": oldString, "new_string": newString})
	if err != nil {
		t.Fatal(err)
	}
	output, err := tool.Invoke(context.Background(), arguments)
	if err != nil {
		t.Fatalf("Invoke(%s) returned a Go error: %v", arguments, err)
	}
	return output
}

func TestEditReplacesASingleMatch(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "a.txt", "alpha\nbeta\ngamma\n")

	output := invokeEdit(t, buildConfinedFileEditTool(root), "a.txt", "beta", "BETA")

	if output.IsError {
		t.Fatalf("unexpected error: %s", output.Content)
	}
	if got := readDisk(t, path); got != "alpha\nBETA\ngamma\n" {
		t.Fatalf("disk = %q", got)
	}
	if !strings.Contains(output.Content, `"replacements":1`) || !strings.Contains(output.Content, `"bytes_written":17`) {
		t.Fatalf("output = %s", output.Content)
	}
}

func TestEditLeavesTheFileUntouchedWhenTheAnchorIsMissingOrAmbiguous(t *testing.T) {
	cases := []struct{ oldString, code string }{
		{"delta", "edit_anchor_not_found"},
		{"a\n", "edit_anchor_ambiguous"},
	}
	for _, tc := range cases {
		root := t.TempDir()
		original := "alpha\nbeta\ngamma\n"
		path := writeFixture(t, root, "a.txt", original)

		output := invokeEdit(t, buildConfinedFileEditTool(root), "a.txt", tc.oldString, "x")

		assertErrorCode(t, output, tc.code)
		if got := readDisk(t, path); got != original {
			t.Fatalf("%s: disk = %q, want unchanged", tc.code, got)
		}
		if tc.code == "edit_anchor_ambiguous" && !strings.Contains(output.Content, `"occurrences":3`) {
			t.Fatalf("output = %s, want occurrences", output.Content)
		}
	}
}

func TestEditKeepsCRLFWhenTheAnchorUsesLF(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "notes", "admin", "clinic-confirmation.md"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := writeFixture(t, root, "clinic.md", string(fixture))

	output := invokeEdit(t, buildConfinedFileEditTool(root), "clinic.md", "Patient: me\nAppointment:", "Patient: me\nRescheduled:")

	if output.IsError {
		t.Fatalf("unexpected error: %s", output.Content)
	}
	got := readDisk(t, path)
	if !strings.Contains(got, "Patient: me\r\nRescheduled:") {
		t.Fatalf("disk = %q, edit not applied", got)
	}
	if strings.Count(got, "\n") != strings.Count(got, "\r\n") {
		t.Fatalf("disk = %q, has a bare LF", got)
	}
}

func TestEditReportsAMissingFileWithAFsWriteHint(t *testing.T) {
	output := invokeEdit(t, buildConfinedFileEditTool(t.TempDir()), "missing.txt", "a", "b")

	assertErrorCode(t, output, "file_not_found")
	if !strings.Contains(output.Content, "fs_write") {
		t.Fatalf("output = %s, want an fs_write hint", output.Content)
	}
}

func TestEditRefusesBinaryAndOversizeFiles(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "bin.dat", "a\x00b")
	writeFixture(t, root, "big.txt", strings.Repeat("x", testMaxBytes+1))
	tool := buildConfinedFileEditTool(root)

	assertErrorCode(t, invokeEdit(t, tool, "bin.dat", "a", "b"), "binary_file")
	assertErrorCode(t, invokeEdit(t, tool, "big.txt", "x", "y"), "content_too_large")
}

func TestEditRejectsAPathOutsideTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "ws")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := writeFixture(t, parent, "outside.txt", "secret\n")

	assertErrorCode(t, invokeEdit(t, buildConfinedFileEditTool(root), "../outside.txt", "secret", "x"), "path_outside_workspace")
	if got := readDisk(t, outside); got != "secret\n" {
		t.Fatalf("outside file = %q, want unchanged", got)
	}
}

func TestEditRejectsInvalidArguments(t *testing.T) {
	tool := buildConfinedFileEditTool(t.TempDir())
	for _, arguments := range []string{
		`{"path":"","old_string":"a","new_string":"b"}`,
		`{"path":"a","old_string":"","new_string":"b"}`,
		`{"path":"a","old_string":"same","new_string":"same"}`,
		`{"path":"a","old_string":"a","new_string":"b","extra":1}`,
	} {
		output, err := tool.Invoke(context.Background(), json.RawMessage(arguments))
		if err != nil {
			t.Fatal(err)
		}
		assertErrorCode(t, output, "invalid_arguments")
	}
}

func TestFileEditToolMeetsTheToolContract(t *testing.T) {
	tooltest.Run(t, buildConfinedFileEditTool(t.TempDir()))
}
