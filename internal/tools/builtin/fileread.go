// Package builtin holds the core tools; every file effect goes through the core.Broker port (D-18).
// Read flow: Invoke → decodeFileReadArgs → broker.OpenFile (resolves, confines, refuses folders) →
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
	"strings"
	"unicode/utf8"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	fileReadToolName    = "fs_read"
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

type FileReadTool struct {
	broker core.Broker
	// workspaceRoot is the scope this tool declares; the broker enforces it.
	workspaceRoot string
	maxBytes      int64
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

func BuildFileReadTool(broker core.Broker, workspaceRoot string, maxBytes int64) *FileReadTool {
	return &FileReadTool{broker: broker, workspaceRoot: workspaceRoot, maxBytes: maxBytes}
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
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": err.Error()}), nil
	}

	file, err := t.broker.OpenFile(ctx, args.Path)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return core.ToolOutput{}, ctxErr
		}
		return buildBrokerErrorOutput(args.Path, t.workspaceRoot, err), nil
	}
	defer file.Content.Close()

	result, err := readLineRange(ctx, file.Content, args.StartLine, args.EndLine, t.maxBytes)
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
	return core.ToolOutput{Content: content, Display: describeRead(file.Info, args)}, nil
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
	if err := decodeStrictArgs(arguments, &args); err != nil {
		return fileReadArgs{}, err
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

// streams line fragments so memory stays bounded by maxBytes even for one huge line.
func readLineRange(ctx context.Context, source io.Reader, startLine, endLine int, maxBytes int64) (lineRangeResult, error) {
	reader := bufio.NewReaderSize(source, readBufferBytes)
	if isBinary, err := hasBinaryHead(reader); err != nil || isBinary {
		return lineRangeResult{isBinary: isBinary}, err
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

// hasBinaryHead reports a NUL in the first binaryScanBytes without consuming them.
func hasBinaryHead(reader *bufio.Reader) (bool, error) {
	head, err := reader.Peek(binaryScanBytes)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return bytes.IndexByte(head, 0) >= 0, nil
}

// ensures cut does not splut multi-byte character
func cutAtRuneBoundary(data []byte, limit int64) string {
	cut := int(limit)
	for cut > 0 && !utf8.RuneStart(data[cut]) {
		cut--
	}
	return string(data[:cut])
}

func describeRead(file core.PathInfo, args fileReadArgs) string {
	shown := file.WorkspacePath
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
