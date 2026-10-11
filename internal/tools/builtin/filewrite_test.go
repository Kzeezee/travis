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

func buildConfinedFileWriteTool(root, newFileLineEnding string) *builtin.FileWriteTool {
	return builtin.BuildFileWriteTool(broker.BuildLocalBroker(root), root, testMaxBytes, newFileLineEnding)
}

func invokeWrite(t *testing.T, tool *builtin.FileWriteTool, path, content string) core.ToolOutput {
	t.Helper()
	arguments, err := json.Marshal(map[string]string{"path": path, "content": content})
	if err != nil {
		t.Fatal(err)
	}
	output, err := tool.Invoke(context.Background(), arguments)
	if err != nil {
		t.Fatalf("Invoke(%s) returned a Go error: %v", arguments, err)
	}
	return output
}

func readDisk(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWriteCreatesANestedNewFileWithTheInjectedLineEnding(t *testing.T) {
	root := t.TempDir()
	output := invokeWrite(t, buildConfinedFileWriteTool(root, "\r\n"), "a/b/new.txt", "one\ntwo\n")

	if output.IsError {
		t.Fatalf("unexpected error: %s", output.Content)
	}
	if got := readDisk(t, filepath.Join(root, "a", "b", "new.txt")); got != "one\r\ntwo\r\n" {
		t.Fatalf("disk = %q", got)
	}
	var result struct {
		Path         string `json:"path"`
		BytesWritten int    `json:"bytes_written"`
		Created      bool   `json:"created"`
	}
	if err := json.Unmarshal([]byte(output.Content), &result); err != nil {
		t.Fatal(err)
	}
	if result.Path != "a/b/new.txt" || result.BytesWritten != 10 || !result.Created {
		t.Fatalf("result = %+v", result)
	}
}

func TestWriteOverwritesAndReportsNotCreated(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "a.txt", "old\n")

	output := invokeWrite(t, buildConfinedFileWriteTool(root, "\r\n"), "a.txt", "new\n")

	if output.IsError || !strings.Contains(output.Content, `"created":false`) {
		t.Fatalf("output = %s", output.Content)
	}
	if got := readDisk(t, path); got != "new\n" {
		t.Fatalf("disk = %q, want the LF file to stay LF", got)
	}
}

func TestWriteKeepsCRLFWhenOverwritingACRLFFile(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "a.txt", "old\r\n")

	invokeWrite(t, buildConfinedFileWriteTool(root, "\n"), "a.txt", "x\ny\r\n")

	if got := readDisk(t, path); got != "x\r\ny\r\n" {
		t.Fatalf("disk = %q", got)
	}
}

func TestWriteRejectsAPathOutsideTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "ws")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}

	output := invokeWrite(t, buildConfinedFileWriteTool(root, "\n"), "../outside.txt", "x")

	assertErrorCode(t, output, "path_outside_workspace")
	if _, err := os.Stat(filepath.Join(parent, "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside file exists or stat failed: %v", err)
	}
}

func TestWriteRejectsOversizeContent(t *testing.T) {
	root := t.TempDir()
	output := invokeWrite(t, buildConfinedFileWriteTool(root, "\n"), "big.txt", strings.Repeat("x", testMaxBytes+1))

	assertErrorCode(t, output, "content_too_large")
	if _, err := os.Stat(filepath.Join(root, "big.txt")); !os.IsNotExist(err) {
		t.Fatalf("big.txt exists or stat failed: %v", err)
	}
}

func TestWriteRejectsInvalidArguments(t *testing.T) {
	tool := buildConfinedFileWriteTool(t.TempDir(), "\n")
	for _, arguments := range []string{`{"path":"","content":"x"}`, `{"path":"a","content":"x","extra":1}`, `{"path":"a","content":1}`} {
		output, err := tool.Invoke(context.Background(), json.RawMessage(arguments))
		if err != nil {
			t.Fatal(err)
		}
		assertErrorCode(t, output, "invalid_arguments")
	}
}

func TestWriteRefusesADirectoryTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	assertErrorCode(t, invokeWrite(t, buildConfinedFileWriteTool(root, "\n"), "dir", "x"), "path_is_directory")
}

func TestWriteReturnsAGoErrorWhenCancelled(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := buildConfinedFileWriteTool(root, "\n").Invoke(ctx, json.RawMessage(`{"path":"a.txt","content":"x"}`)); err == nil {
		t.Fatal("expected a Go error for a cancelled context")
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("a.txt exists or stat failed: %v", err)
	}
}

func TestFileWriteToolMeetsTheToolContract(t *testing.T) {
	tooltest.Run(t, buildConfinedFileWriteTool(t.TempDir(), "\n"))
}

func TestDescribeReportsTheWriteCapability(t *testing.T) {
	root := t.TempDir()
	spec := buildConfinedFileWriteTool(root, "\n").Describe()

	if spec.Name != "fs_write" {
		t.Fatalf("Describe().Name = %q", spec.Name)
	}
	if len(spec.Capabilities) != 1 || spec.Capabilities[0].Kind != core.CapabilityFSWrite || spec.Capabilities[0].Scope != root {
		t.Fatalf("Capabilities = %+v", spec.Capabilities)
	}
}
