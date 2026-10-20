package core

import (
	"encoding/json"
	"errors"
	"reflect"
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

func TestMessageJSONRoundTripsEveryPartKind(t *testing.T) {
	message := Message{Role: RoleAssistant, Parts: []Part{
		TextPart{Text: "reading file"},
		ToolCallPart{ToolCall: ToolCall{ID: "call_123", Name: "fs_read", Arguments: json.RawMessage(`{"path":"a.go"}`), ProviderExtra: json.RawMessage(`{"google":{"thought_signature":"c2ln"}}`)}},
		ToolResultPart{ToolResult: ToolResult{CallID: "call_123", Name: "fs_read", Content: "package a", IsError: true}},
	}}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Message
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(decoded, message) {
		t.Fatalf("decoded = %+v, want %+v", decoded, message)
	}
}

func TestMessageUnmarshalJSONRefusesUnknownKindsAndFields(t *testing.T) {
	for name, encoded := range map[string]string{
		"unknown kind":       `{"role":"user","parts":[{"kind":"image","url":"x"}]}`,
		"unknown part field": `{"role":"user","parts":[{"kind":"text","text":"hi","extra":1}]}`,
		"unknown field":      `{"role":"user","parts":[],"author":"broker"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var decoded Message
			if err := json.Unmarshal([]byte(encoded), &decoded); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Unmarshal = %v, want ErrInvalid", err)
			}
		})
	}
}
