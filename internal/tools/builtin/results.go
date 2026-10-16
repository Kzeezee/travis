package builtin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Kzeezee/travis/internal/core"
)

// Error codes shared by the built-in tools; they are model-facing, so they stay stable.
const (
	errorInvalidArguments    = "invalid_arguments"
	errorPermissionDenied    = "permission_denied"
	errorFileNotFound        = "file_not_found"
	errorPathIsDirectory     = "path_is_directory"
	errorBinaryFile          = "binary_file"
	errorReadFailed          = "read_failed"
	errorWriteFailed         = "write_failed"
	errorContentTooLarge     = "content_too_large"
	errorEditAnchorNotFound  = "edit_anchor_not_found"
	errorEditAnchorAmbiguous = "edit_anchor_ambiguous"
)

// decodeStrictArgs rejects unknown fields and trailing data, so a malformed call errors instead of half-running.
func decodeStrictArgs(arguments json.RawMessage, args any) error {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(args); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the arguments object")
	}
	return nil
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

// buildBrokerErrorOutput tells the model why the broker refused or failed a path (or why
// readWholeFileCapped rejected it), in the stable codes above, whichever broker is behind
// the port; anything unrecognised reports fallbackCode.
func buildBrokerErrorOutput(requested, workspaceRoot, fallbackCode string, err error) core.ToolOutput {
	switch {
	case errors.Is(err, core.ErrDenied):
		return buildErrorOutput(errorPermissionDenied, map[string]any{"path": requested, "workspace": workspaceRoot, "detail": err.Error()})
	case errors.Is(err, core.ErrNotFound):
		return buildErrorOutput(errorFileNotFound, map[string]any{"path": requested})
	case errors.Is(err, core.ErrIsDirectory):
		return buildErrorOutput(errorPathIsDirectory, map[string]any{"path": requested})
	case errors.Is(err, core.ErrInvalid):
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": err.Error()})
	case errors.Is(err, errContentTooLarge):
		return buildErrorOutput(errorContentTooLarge, map[string]any{"path": requested, "detail": err.Error()})
	case errors.Is(err, errBinaryContent):
		return buildErrorOutput(errorBinaryFile, map[string]any{"path": requested})
	default:
		return buildErrorOutput(fallbackCode, map[string]any{"path": requested, "detail": err.Error()})
	}
}
