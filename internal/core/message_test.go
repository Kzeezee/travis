package core

import (
	"encoding/json"
	"testing"
)

func TestPartsReportPartKind(t *testing.T) {
	tests := []struct {
		name string
		part Part
		want PartKind
	}{
		{name: "text", part: TextPart{}, want: PartText},
		{name: "tool call", part: ToolCallPart{}, want: PartToolCall},
		{name: "tool result", part: ToolResultPart{}, want: PartToolResult},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.part.ReportPartKind(); got != tt.want {
				t.Fatalf("ReportPartKind() = %q, want %q", got, tt.want)
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
					Name: "fs.read",
				},
			},
		},
	}

	got, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"role":"assistant","parts":[{"kind":"text","text":"reading file"},{"kind":"tool_call","tool_call":{"id":"call_123","name":"fs.read","arguments":null}}]}`
	if string(got) != want {
		t.Fatalf("json.Marshal(Message) = %s, want %s", got, want)
	}
}
