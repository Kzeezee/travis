package core

import (
	"encoding/json"
	"testing"
)

func TestPartsReportMessageContentType(t *testing.T) {
	tests := []struct {
		name string
		part Part
		want MessageContentType
	}{
		{name: "text", part: TextPart{}, want: MessageContentText},
		{name: "tool call", part: ToolCallPart{}, want: MessageContentToolCall},
		{name: "tool result", part: ToolResultPart{}, want: MessageContentToolResult},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.part.ReportMessageContentType(); got != tt.want {
				t.Fatalf("ReportMessageContentType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMessageMarshalJSONWithStrictParts(t *testing.T) {
	message := Message{
		Role: RoleAssistant,
		Parts: []Part{
			TextPart{Text: "reading file"},
			ToolCallPart{
				ToolCall: ToolCall{
					ID:   ID("call_123"),
					Name: "fs_read",
				},
			},
		},
	}

	got, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"role":"assistant","parts":[{"kind":"text","text":"reading file"},{"kind":"tool_call","tool_call":{"id":"call_123","name":"fs_read","arguments":null}}]}`
	if string(got) != want {
		t.Fatalf("json.Marshal(Message) = %s, want %s", got, want)
	}
}
