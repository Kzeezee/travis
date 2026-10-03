package builtin

import (
	"encoding/json"
	"fmt"

	"github.com/Kzeezee/travis/internal/core"
)

// Error codes shared by the built-in tools; they are model-facing, so they stay stable.
const (
	errorInvalidArguments     = "invalid_arguments"
	errorPathOutsideWorkspace = "path_outside_workspace"
	errorFileNotFound         = "file_not_found"
	errorPathIsDirectory      = "path_is_directory"
	errorBinaryFile           = "binary_file"
	errorReadFailed           = "read_failed"
)

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
