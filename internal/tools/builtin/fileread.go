// Package builtin holds the core tools.
// Phase 1 tools perform their own IO; D-18's broker swap happens in Phase 2A.
// Read flow: Invoke → decodeFileReadArgs → resolveWorkspacePath (which uses isInsideRoot) → check it isn't a folder → open the file →
// readLineRange: file → 64 KiB read buffer (refills as it is used; first 8 KiB checked for NUL) → one line at a time,
// lines before start_line dropped, the rest numbered into the output buffer (grows up to maxBytes, cut by cutAtRuneBoundary) →
// describeRead if it worked, or buildErrorOutput at whichever step it failed.
package builtin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

const (
	fileReadToolName    = "fs.read"
	fileReadDescription = "Read a UTF-8 text file inside the workspace. Paths are relative to the workspace root. " +
		"Optionally restrict output to a 1-based inclusive line range. " +
		"Each output line is prefixed with its line number and a tab; the prefix is not part of the file."
	fileReadSchema = `{"type":"object","properties":{` +
		`"path":{"type":"string","description":"File path relative to the workspace root"},` +
		`"start_line":{"type":"integer","minimum":1,"description":"First line to return, 1-based"},` +
		`"end_line":{"type":"integer","minimum":1,"description":"Last line to return, inclusive"}},` +
		`"required":["path"],"additionalProperties":false}`

	binaryScanBytes = 8 << 10
	readBufferBytes = 64 << 10
)

const (
	errorInvalidArguments     = "invalid_arguments"
	errorPathOutsideWorkspace = "path_outside_workspace"
	errorFileNotFound         = "file_not_found"
	errorPathIsDirectory      = "path_is_directory"
	errorBinaryFile           = "binary_file"
	errorReadFailed           = "read_failed"
)

var expectedFileReadArgs = map[string]string{
	"path":       "string (required)",
	"start_line": "int",
	"end_line":   "int",
}

type FileReadTool struct {
	paths         config.PathSet
	workspaceRoot string
	// resolvedRoot is workspaceRoot with symlinks evaluated, so confinement compares like with like.
	resolvedRoot string
	maxBytes     int64
}

type fileReadArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

type lineRangeResult struct {
	text     string
	isBinary bool
	// truncatedAtLine is the partly shown line where the byte cap was hit; 0 means not truncated.
	truncatedAtLine int
	// totalLines is set only when the whole file was read; 0 means an empty file or an early stop.
	totalLines int
}

func BuildFileReadTool(workspaceRoot string, maxBytes int64) *FileReadTool {
	root := filepath.Clean(workspaceRoot)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot = root
	}
	return &FileReadTool{
		paths:         config.PathSet{WorkspaceDir: root},
		workspaceRoot: root,
		resolvedRoot:  resolvedRoot,
		maxBytes:      maxBytes,
	}
}

func (t *FileReadTool) Name() string {
	return fileReadToolName
}

func (t *FileReadTool) Describe() core.ToolSpec {
	return core.ToolSpec{
		Name:         fileReadToolName,
		Description:  fileReadDescription,
		InputSchema:  json.RawMessage(fileReadSchema),
		Capabilities: []core.Capability{{Kind: core.CapabilityFSRead, Scope: t.workspaceRoot}},
	}
}

func (t *FileReadTool) Invoke(ctx context.Context, arguments json.RawMessage) (core.ToolOutput, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolOutput{}, err
	}

	args, err := decodeFileReadArgs(arguments)
	if err != nil {
		return buildErrorOutput(errorInvalidArguments, map[string]any{
			"detail":   err.Error(),
			"expected": expectedFileReadArgs,
		}), nil
	}

	target, rejection, isAllowed := t.resolveWorkspacePath(args.Path)
	if !isAllowed {
		return rejection, nil
	}

	info, err := os.Stat(target)
	if err != nil {
		return buildErrorOutput(errorReadFailed, map[string]any{"path": args.Path, "detail": err.Error()}), nil
	}
	if info.IsDir() {
		return buildErrorOutput(errorPathIsDirectory, map[string]any{"path": args.Path}), nil
	}

	// path is confined to the workspace root by resolveWorkspacePath; replaced by Broker in Phase 2
	file, err := os.Open(target)
	if err != nil {
		return buildErrorOutput(errorReadFailed, map[string]any{"path": args.Path, "detail": err.Error()}), nil
	}
	defer file.Close()

	result, err := readLineRange(ctx, file, args.StartLine, args.EndLine, t.maxBytes)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return core.ToolOutput{}, ctxErr
		}
		return buildErrorOutput(errorReadFailed, map[string]any{"path": args.Path, "detail": err.Error()}), nil
	}
	if result.isBinary {
		return buildErrorOutput(errorBinaryFile, map[string]any{"path": args.Path}), nil
	}

	content := result.text
	if content == "" {
		content = describeEmptyRead(args.StartLine, result.totalLines)
	}
	if result.truncatedAtLine > 0 {
		content += fmt.Sprintf("\n[truncated at line %d: output exceeds %d bytes; continue with start_line=%d]",
			result.truncatedAtLine, t.maxBytes, result.truncatedAtLine)
	}
	return core.ToolOutput{Content: content, Display: t.describeRead(target, args)}, nil
}

// empty output is explained so the model can correct its range instead of guessing
func describeEmptyRead(startLine, totalLines int) string {
	if totalLines == 0 {
		return "[file is empty]"
	}
	return fmt.Sprintf("[start_line %d is past the end of the file; it has %d lines]", startLine, totalLines)
}

// only accepts valid path, start_line and end_line args
func decodeFileReadArgs(arguments json.RawMessage) (fileReadArgs, error) {
	var args fileReadArgs
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return fileReadArgs{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fileReadArgs{}, errors.New("unexpected data after the arguments object")
	}

	if strings.TrimSpace(args.Path) == "" {
		return fileReadArgs{}, errors.New("path is required")
	}
	if args.StartLine < 0 || args.EndLine < 0 {
		return fileReadArgs{}, errors.New("line numbers start at 1")
	}
	if args.StartLine > 0 && args.EndLine > 0 && args.EndLine < args.StartLine {
		return fileReadArgs{}, errors.New("end_line is before start_line")
	}
	return args, nil
}

// takes the path the model asked for, turns it into a real path on disk, and allows it only if that path is inside the workspace
// can move inside Broker during Phase 2
func (t *FileReadTool) resolveWorkspacePath(requested string) (string, core.ToolOutput, bool) {
	outside := buildErrorOutput(errorPathOutsideWorkspace, map[string]any{
		"path":      requested,
		"workspace": t.workspaceRoot,
	})

	resolved, err := t.paths.Resolve(requested)
	if err != nil {
		return "", buildErrorOutput(errorInvalidArguments, map[string]any{
			"detail":   err.Error(),
			"expected": expectedFileReadArgs,
		}), false
	}
	if !filepath.IsAbs(resolved) || !isInsideRoot(t.workspaceRoot, resolved) {
		return "", outside, false
	}

	evaluated, err := filepath.EvalSymlinks(resolved)
	if errors.Is(err, fs.ErrNotExist) {
		return "", buildErrorOutput(errorFileNotFound, map[string]any{"path": requested}), false
	}
	if err != nil {
		return "", buildErrorOutput(errorReadFailed, map[string]any{"path": requested, "detail": err.Error()}), false
	}
	if !isInsideRoot(t.resolvedRoot, evaluated) {
		return "", outside, false
	}
	return evaluated, core.ToolOutput{}, true
}

func isInsideRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// streams line fragments so memory stays bounded by maxBytes even for one huge line.
func readLineRange(ctx context.Context, source io.Reader, startLine, endLine int, maxBytes int64) (lineRangeResult, error) {
	reader := bufio.NewReaderSize(source, readBufferBytes)
	head, err := reader.Peek(binaryScanBytes)
	if err != nil && !errors.Is(err, io.EOF) {
		return lineRangeResult{}, err
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return lineRangeResult{isBinary: true}, nil
	}

	if startLine == 0 {
		startLine = 1
	}
	var output bytes.Buffer
	isAtLineStart := true
	for line := 1; endLine == 0 || line <= endLine; {
		if err := ctx.Err(); err != nil {
			return lineRangeResult{}, err
		}
		fragment, readErr := reader.ReadSlice('\n')
		if line >= startLine && len(fragment) > 0 {
			if isAtLineStart {
				fmt.Fprintf(&output, "%d\t", line)
			}
			output.Write(fragment)
			if int64(output.Len()) > maxBytes {
				return lineRangeResult{text: cutAtRuneBoundary(output.Bytes(), maxBytes), truncatedAtLine: line}, nil
			}
		}
		// An empty fragment carries no line information, so it leaves isAtLineStart as it was.
		if len(fragment) > 0 {
			isAtLineStart = fragment[len(fragment)-1] == '\n'
			if isAtLineStart {
				line++
			}
		}
		if errors.Is(readErr, io.EOF) {
			totalLines := line
			if isAtLineStart {
				totalLines = line - 1
			}
			return lineRangeResult{text: output.String(), totalLines: totalLines}, nil
		}
		if readErr != nil && !errors.Is(readErr, bufio.ErrBufferFull) {
			return lineRangeResult{}, readErr
		}
	}
	return lineRangeResult{text: output.String()}, nil
}

// ensures cut does not splut multi-byte character
func cutAtRuneBoundary(data []byte, limit int64) string {
	cut := int(limit)
	for cut > 0 && !utf8.RuneStart(data[cut]) {
		cut--
	}
	return string(data[:cut])
}

func (t *FileReadTool) describeRead(target string, args fileReadArgs) string {
	shown := args.Path
	if rel, err := filepath.Rel(t.resolvedRoot, target); err == nil {
		shown = filepath.ToSlash(rel)
	}
	if args.StartLine == 0 && args.EndLine == 0 {
		return "read " + shown
	}
	start, end := "1", "end"
	if args.StartLine > 0 {
		start = fmt.Sprint(args.StartLine)
	}
	if args.EndLine > 0 {
		end = fmt.Sprint(args.EndLine)
	}
	return fmt.Sprintf("read %s (lines %s-%s)", shown, start, end)
}

func buildErrorOutput(code string, fields map[string]any) core.ToolOutput {
	payload := map[string]any{"error": code}
	for key, value := range fields {
		payload[key] = value
	}
	content, err := json.Marshal(payload)
	if err != nil {
		content = fmt.Appendf(nil, `{"error":%q}`, code)
	}
	// The frontend already labels the card with the tool name, so Display carries only the cause.
	return core.ToolOutput{Content: string(content), Display: code, IsError: true}
}
