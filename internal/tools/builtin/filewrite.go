package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	fileWriteToolName    = "fs_write"
	fileWriteDescription = "Create or overwrite a UTF-8 text file inside the workspace with the given content. " +
		"Paths are relative to the workspace root; missing parent directories are created. " +
		"An existing file keeps its line-ending convention. Prefer fs_edit to change part of an existing file."
	fileWriteSchema = `{"type":"object","properties":{` +
		`"path":{"type":"string","description":"File path relative to the workspace root"},` +
		`"content":{"type":"string","description":"The complete new file content"}},` +
		`"required":["path","content"],"additionalProperties":false}`
)

type FileWriteTool struct {
	broker core.Broker
	// workspaceRoot is the scope this tool declares; the broker enforces it.
	workspaceRoot string
	maxBytes      int64
	// newFileLineEnding is the OS convention, injected so this package stays OS-agnostic.
	newFileLineEnding string
}

type fileWriteArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func BuildFileWriteTool(broker core.Broker, workspaceRoot string, maxBytes int64, newFileLineEnding string) *FileWriteTool {
	return &FileWriteTool{broker: broker, workspaceRoot: workspaceRoot, maxBytes: maxBytes, newFileLineEnding: newFileLineEnding}
}

func (t *FileWriteTool) Name() string {
	return fileWriteToolName
}

func (t *FileWriteTool) Describe() core.ToolSpec {
	return core.ToolSpec{
		Name:         fileWriteToolName,
		Description:  fileWriteDescription,
		InputSchema:  json.RawMessage(fileWriteSchema),
		Capabilities: []core.Capability{{Kind: core.CapabilityFSWrite, Scope: t.workspaceRoot}},
	}
}

func (t *FileWriteTool) Invoke(ctx context.Context, arguments json.RawMessage) (core.ToolOutput, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolOutput{}, err
	}

	var args fileWriteArgs
	if err := decodeStrictArgs(arguments, &args); err != nil {
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": err.Error()}), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": "path is required"}), nil
	}
	if int64(len(args.Content)) > t.maxBytes {
		return buildErrorOutput(errorContentTooLarge, map[string]any{
			"path": args.Path, "detail": fmt.Sprintf("content exceeds %d bytes", t.maxBytes),
		}), nil
	}

	eol, isCreated, err := t.selectLineEnding(ctx, args.Path)
	if err == nil {
		var info core.PathInfo
		content := []byte(applyLineEnding(args.Content, eol))
		if info, err = t.broker.WriteFile(ctx, args.Path, content); err == nil {
			return describeWrite(info, len(content), isCreated), nil
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return core.ToolOutput{}, ctxErr
	}
	return buildBrokerErrorOutput(args.Path, t.workspaceRoot, errorWriteFailed, err), nil
}

// selectLineEnding keeps an existing file's convention and uses the injected default for a new
// file or one with no line breaks yet; it also reports whether the write creates the file.
func (t *FileWriteTool) selectLineEnding(ctx context.Context, path string) (string, bool, error) {
	file, err := t.broker.OpenFile(ctx, path)
	if errors.Is(err, core.ErrNotFound) {
		return t.newFileLineEnding, true, nil
	}
	if err != nil {
		return "", false, err
	}
	defer file.Content.Close()

	head := make([]byte, readBufferBytes)
	n, err := io.ReadFull(file.Content, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", false, err
	}
	if !bytes.Contains(head[:n], []byte("\n")) {
		return t.newFileLineEnding, false, nil
	}
	return detectLineEnding(head[:n]), false, nil
}

func describeWrite(info core.PathInfo, bytesWritten int, isCreated bool) core.ToolOutput {
	content, _ := json.Marshal(map[string]any{
		"path": info.WorkspacePath, "bytes_written": bytesWritten, "created": isCreated,
	})
	verb := "wrote "
	if isCreated {
		verb = "created "
	}
	return core.ToolOutput{Content: string(content), Display: verb + info.WorkspacePath}
}
