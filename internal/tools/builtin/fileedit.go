package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	fileEditToolName    = "fs_edit"
	fileEditDescription = "Replace exactly one occurrence of old_string with new_string in an existing text file inside the workspace. " +
		"old_string must match the file exactly once; include enough surrounding lines to make it unique. " +
		"The file keeps its line-ending convention. Use fs_write to create a new file."
	fileEditSchema = `{"type":"object","properties":{` +
		`"path":{"type":"string","description":"File path relative to the workspace root"},` +
		`"old_string":{"type":"string","description":"Exact text to replace; must occur exactly once"},` +
		`"new_string":{"type":"string","description":"Replacement text"}},` +
		`"required":["path","old_string","new_string"],"additionalProperties":false}`
	editAnchorHint = "re-read the file with fs_read and include more surrounding context in old_string"
)

type FileEditTool struct {
	broker core.Broker
	// workspaceRoot is the scope this tool declares; the broker enforces it.
	workspaceRoot string
	maxBytes      int64
}

type fileEditArgs struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

func BuildFileEditTool(broker core.Broker, workspaceRoot string, maxBytes int64) *FileEditTool {
	return &FileEditTool{broker: broker, workspaceRoot: workspaceRoot, maxBytes: maxBytes}
}

func (t *FileEditTool) Name() string {
	return fileEditToolName
}

func (t *FileEditTool) Describe() core.ToolSpec {
	return core.ToolSpec{
		Name:         fileEditToolName,
		Description:  fileEditDescription,
		InputSchema:  json.RawMessage(fileEditSchema),
		Capabilities: []core.Capability{{Kind: core.CapabilityFSWrite, Scope: t.workspaceRoot}},
	}
}

func (t *FileEditTool) Invoke(ctx context.Context, arguments json.RawMessage) (core.ToolOutput, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolOutput{}, err
	}

	var args fileEditArgs
	if err := decodeStrictArgs(arguments, &args); err != nil {
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": err.Error()}), nil
	}
	if output, isInvalid := t.validateArgs(args); isInvalid {
		return output, nil
	}

	data, err := readWholeFileCapped(ctx, t.broker, args.Path, t.maxBytes)
	if err == nil {
		eol := detectLineEnding(data)
		edited, occurrences := spliceSingleOccurrence(applyLineEnding(string(data), "\n"),
			applyLineEnding(args.OldString, "\n"), applyLineEnding(args.NewString, "\n"))
		if occurrences != 1 {
			return buildAnchorErrorOutput(args.Path, occurrences), nil
		}
		var info core.PathInfo
		content := []byte(applyLineEnding(edited, eol))
		if info, err = t.broker.WriteFile(ctx, args.Path, content); err == nil {
			return describeEdit(info, len(content)), nil
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return core.ToolOutput{}, ctxErr
	}
	if errors.Is(err, core.ErrNotFound) {
		return buildErrorOutput(errorFileNotFound, map[string]any{
			"path": args.Path, "hint": "fs_edit only changes existing files; use fs_write to create one",
		}), nil
	}
	return buildBrokerErrorOutput(args.Path, t.workspaceRoot, errorWriteFailed, err), nil
}

func (t *FileEditTool) validateArgs(args fileEditArgs) (core.ToolOutput, bool) {
	switch {
	case strings.TrimSpace(args.Path) == "":
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": "path is required"}), true
	case args.OldString == "":
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": "old_string must not be empty"}), true
	case args.OldString == args.NewString:
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": "old_string and new_string are identical"}), true
	case int64(len(args.OldString)) > t.maxBytes || int64(len(args.NewString)) > t.maxBytes:
		return buildErrorOutput(errorContentTooLarge, map[string]any{
			"path": args.Path, "detail": fmt.Sprintf("old_string or new_string exceeds %d bytes", t.maxBytes),
		}), true
	}
	return core.ToolOutput{}, false
}

// spliceSingleOccurrence replaces old with replacement only when old occurs exactly once,
// returning the file unchanged otherwise, along with the occurrence count.
func spliceSingleOccurrence(file, old, replacement string) (string, int) {
	occurrences := strings.Count(file, old)
	if occurrences != 1 {
		return file, occurrences
	}
	return strings.Replace(file, old, replacement, 1), occurrences
}

func buildAnchorErrorOutput(path string, occurrences int) core.ToolOutput {
	if occurrences == 0 {
		return buildErrorOutput(errorEditAnchorNotFound, map[string]any{"path": path, "hint": editAnchorHint})
	}
	return buildErrorOutput(errorEditAnchorAmbiguous, map[string]any{
		"path": path, "occurrences": occurrences, "hint": editAnchorHint,
	})
}

func describeEdit(info core.PathInfo, bytesWritten int) core.ToolOutput {
	content, _ := json.Marshal(map[string]any{
		"path": info.WorkspacePath, "bytes_written": bytesWritten, "replacements": 1,
	})
	return core.ToolOutput{Content: string(content), Display: "edited " + info.WorkspacePath}
}
