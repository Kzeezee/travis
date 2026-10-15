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

const testMaxBytes = 64 << 10

func writeFixture(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// buildConfinedFileReadTool runs the tool over a LocalBroker, so these tests cover the read
// path end to end on a real temporary workspace.
func buildConfinedFileReadTool(root string, maxBytes int64) *builtin.FileReadTool {
	return builtin.BuildFileReadTool(broker.BuildLocalBroker(root, core.SystemClock{}), root, maxBytes)
}

func invokeRead(t *testing.T, tool *builtin.FileReadTool, arguments string) core.ToolOutput {
	t.Helper()
	output, err := tool.Invoke(context.Background(), json.RawMessage(arguments))
	if err != nil {
		t.Fatalf("Invoke(%s) returned a Go error: %v", arguments, err)
	}
	return output
}

func mustMarshalPath(t *testing.T, path string) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func assertContent(t *testing.T, output core.ToolOutput, want string) {
	t.Helper()
	if output.IsError {
		t.Fatalf("unexpected error result: %s", output.Content)
	}
	if output.Content != want {
		t.Fatalf("Content = %q, want %q", output.Content, want)
	}
}

func assertErrorCode(t *testing.T, output core.ToolOutput, want string) {
	t.Helper()
	if !output.IsError {
		t.Fatalf("expected error %q, got content %q", want, output.Content)
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(output.Content), &payload); err != nil {
		t.Fatalf("error payload is not JSON: %q", output.Content)
	}
	if payload.Error != want {
		t.Fatalf("error = %q, want %q (payload %s)", payload.Error, want, output.Content)
	}
}

func TestReadReturnsANestedFile(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "docs/notes/todo.txt", "alpha\nbeta\n")
	tool := buildConfinedFileReadTool(root, testMaxBytes)

	output := invokeRead(t, tool, `{"path":"docs/notes/todo.txt"}`)

	assertContent(t, output, "1\talpha\n2\tbeta\n")
	if output.Display != "read docs/notes/todo.txt" {
		t.Fatalf("Display = %q", output.Display)
	}
}

func TestReadAcceptsTheWorkspacePrefix(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.txt", "hello")
	tool := buildConfinedFileReadTool(root, testMaxBytes)

	assertContent(t, invokeRead(t, tool, `{"path":"workspace:a.txt"}`), "1\thello")
}

func TestReadSelectsLineRanges(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "lines.txt", "one\ntwo\nthree\nfour\n")
	tool := buildConfinedFileReadTool(root, testMaxBytes)

	cases := []struct {
		arguments string
		want      string
	}{
		{`{"path":"lines.txt","start_line":2,"end_line":3}`, "2\ttwo\n3\tthree\n"},
		{`{"path":"lines.txt","start_line":3}`, "3\tthree\n4\tfour\n"},
		{`{"path":"lines.txt","end_line":1}`, "1\tone\n"},
		{`{"path":"lines.txt","start_line":3,"end_line":99}`, "3\tthree\n4\tfour\n"},
		{`{"path":"lines.txt","start_line":50}`, "[start_line 50 is past the end of the file; it has 4 lines]"},
	}
	for _, tc := range cases {
		t.Run(tc.arguments, func(t *testing.T) {
			assertContent(t, invokeRead(t, tool, tc.arguments), tc.want)
		})
	}
}

func TestReadCountsAFinalLineWithoutANewline(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "lines.txt", "one\ntwo\nthree\nfour")
	tool := buildConfinedFileReadTool(root, testMaxBytes)

	assertContent(t, invokeRead(t, tool, `{"path":"lines.txt","start_line":9}`),
		"[start_line 9 is past the end of the file; it has 4 lines]")
}

func TestReadExplainsAnEmptyFile(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "empty.txt", "")
	tool := buildConfinedFileReadTool(root, testMaxBytes)

	assertContent(t, invokeRead(t, tool, `{"path":"empty.txt"}`), "[file is empty]")
	assertContent(t, invokeRead(t, tool, `{"path":"empty.txt","start_line":3}`), "[file is empty]")
}

func TestReadRejectsPathsOutsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := writeFixture(t, t.TempDir(), "secret.txt", "secret")
	tool := buildConfinedFileReadTool(root, testMaxBytes)

	for _, arguments := range []string{
		`{"path":"../secret.txt"}`,
		`{"path":"../../../../../../etc/passwd"}`,
		mustMarshalPath(t, outside),
	} {
		t.Run(arguments, func(t *testing.T) {
			assertErrorCode(t, invokeRead(t, tool, arguments), "permission_denied")
		})
	}
}

func TestReadRejectsSymlinksThatEscapeTheWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := writeFixture(t, t.TempDir(), "secret.txt", "secret")
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	tool := buildConfinedFileReadTool(root, testMaxBytes)

	assertErrorCode(t, invokeRead(t, tool, `{"path":"link.txt"}`), "permission_denied")
}

func TestReadReportsMissingFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "dir/file.txt", "x")
	tool := buildConfinedFileReadTool(root, testMaxBytes)

	assertErrorCode(t, invokeRead(t, tool, `{"path":"missing.txt"}`), "file_not_found")
	assertErrorCode(t, invokeRead(t, tool, `{"path":"dir"}`), "path_is_directory")
}

func TestReadRejectsBinaryFiles(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "image.bin", "PNG\x00\x01\x02")
	tool := buildConfinedFileReadTool(root, testMaxBytes)

	assertErrorCode(t, invokeRead(t, tool, `{"path":"image.bin"}`), "binary_file")
}

func TestReadTruncatesOversizeFilesAndSaysWhereToContinue(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "big.txt", "aaaa\nbbbb\ncccc\ndddd\n")
	tool := buildConfinedFileReadTool(root, 17)

	output := invokeRead(t, tool, `{"path":"big.txt"}`)

	assertContent(t, output, "1\taaaa\n2\tbbbb\n3\tc"+
		"\n[truncated at line 3: output exceeds 17 bytes; continue with start_line=3]")
}

func TestReadDoesNotMarkAnExactFitAsTruncated(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "exact.txt", "0123456789")
	tool := buildConfinedFileReadTool(root, 12)

	assertContent(t, invokeRead(t, tool, `{"path":"exact.txt"}`), "1\t0123456789")
}

func TestReadNumbersALongLineOnce(t *testing.T) {
	root := t.TempDir()
	longLine := strings.Repeat("x", 100<<10)
	writeFixture(t, root, "long.txt", longLine+"\nshort\n")
	tool := buildConfinedFileReadTool(root, 1<<20)

	assertContent(t, invokeRead(t, tool, `{"path":"long.txt"}`), "1\t"+longLine+"\n2\tshort\n")
}

func TestReadRejectsInvalidArguments(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.txt", "a")
	tool := buildConfinedFileReadTool(root, testMaxBytes)

	for _, arguments := range []string{
		`{"pathh":"a.txt"}`,
		`[]`,
		`"str"`,
		`null`,
		`{}`,
		`{"path":"a.txt"} {"path":"a.txt"}`,
		`{"path":"a.txt","start_line":3,"end_line":1}`,
		`{"path":"a.txt","start_line":-1}`,
		`{"path":"a.txt","start_line":"1"}`,
		`{"path":"nope:a.txt"}`,
	} {
		t.Run(arguments, func(t *testing.T) {
			assertErrorCode(t, invokeRead(t, tool, arguments), "invalid_arguments")
		})
	}
}

func TestReadReturnsAGoErrorWhenCancelled(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.txt", "a")
	tool := buildConfinedFileReadTool(root, testMaxBytes)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := tool.Invoke(ctx, json.RawMessage(`{"path":"a.txt"}`)); err == nil {
		t.Fatal("expected a Go error for a cancelled context")
	}
}

func TestFileReadToolMeetsTheToolContract(t *testing.T) {
	tooltest.Run(t, buildConfinedFileReadTool(t.TempDir(), testMaxBytes))
}

func TestDescribeReportsTheReadCapability(t *testing.T) {
	root := t.TempDir()
	tool := buildConfinedFileReadTool(root, testMaxBytes)
	spec := tool.Describe()

	if spec.Name != "fs_read" {
		t.Fatalf("Describe().Name = %q", spec.Name)
	}
	if len(spec.Capabilities) != 1 || spec.Capabilities[0].Kind != core.CapabilityFSRead ||
		spec.Capabilities[0].Scope != filepath.Clean(root) {
		t.Fatalf("Capabilities = %+v", spec.Capabilities)
	}
}
