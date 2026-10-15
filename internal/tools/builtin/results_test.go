package builtin

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

func TestBrokerDenialsPreserveTheirActualReason(t *testing.T) {
	for _, reason := range []string{
		`fs_read "demo.txt" was denied by the user`,
		`fs_write "demo.txt" was denied by the user`,
		`refused by policy (deny rule)`,
		`"../demo.txt" is outside the workspace`,
	} {
		t.Run(reason, func(t *testing.T) {
			err := fmt.Errorf("%s: %w", reason, core.ErrDenied)
			output := buildBrokerErrorOutput("demo.txt", "workspace", "write_failed", err)
			var result struct {
				Error     string `json:"error"`
				Path      string `json:"path"`
				Workspace string `json:"workspace"`
				Detail    string `json:"detail"`
			}
			if decodeErr := json.Unmarshal([]byte(output.Content), &result); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if !output.IsError || output.Display != "permission_denied" || result.Error != "permission_denied" {
				t.Fatalf("denial output = %+v", output)
			}
			if result.Path != "demo.txt" || result.Workspace != "workspace" || result.Detail != err.Error() {
				t.Fatalf("denial lost its context or reason: %+v", result)
			}
		})
	}
}
