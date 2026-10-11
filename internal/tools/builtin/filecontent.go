package builtin

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

var (
	errContentTooLarge = errors.New("content too large")
	errBinaryContent   = errors.New("binary content")
)

// detectLineEnding reports CRLF if the data uses it anywhere, so edits keep a Windows file's convention.
func detectLineEnding(data []byte) string {
	if bytes.Contains(data, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// applyLineEnding normalises text to LF, then converts every line break to eol.
func applyLineEnding(text, eol string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", eol)
}

// readWholeFileCapped reads a text file through the broker, failing with errContentTooLarge
// past maxBytes and errBinaryContent on a NUL in the head, instead of loading either.
func readWholeFileCapped(ctx context.Context, broker core.Broker, path string, maxBytes int64) ([]byte, error) {
	file, err := broker.OpenFile(ctx, path)
	if err != nil {
		return nil, err
	}
	defer file.Content.Close()

	reader := bufio.NewReaderSize(io.LimitReader(file.Content, maxBytes+1), readBufferBytes)
	if isBinary, err := hasBinaryHead(reader); err != nil {
		return nil, err
	} else if isBinary {
		return nil, errBinaryContent
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: file exceeds %d bytes", errContentTooLarge, maxBytes)
	}
	return data, ctx.Err()
}
