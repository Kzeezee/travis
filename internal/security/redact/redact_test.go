package redact_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/redact"
)

func TestRedactTextCatchesKnownSecretFormats(t *testing.T) {
	cases := map[string]string{
		"openai key":      "key is sk-abcdefghijklmnopqrstuvwxyz",
		"aws access key":  "id is AKIAABCDEFGHIJKLMNOP",
		"github token":    "token ghp_abcdefghijklmnopqrstuvwx",
		"slack token":     "token xoxb-1234567890-abcdefghij",
		"bearer header":   "Authorization: Bearer abc123def456ghi789",
		"jwt":             "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"pem private key": "-----BEGIN RSA PRIVATE KEY-----\nMIIBOw==\n-----END RSA PRIVATE KEY-----",
	}
	r := redact.New()
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			got := r.RedactText(text)
			if !strings.Contains(got, redact.Placeholder) {
				t.Fatalf("RedactText(%q) = %q, want it to contain %q", text, got, redact.Placeholder)
			}
		})
	}
}

func TestRedactTextScrubsKnownValuesRegardlessOfShape(t *testing.T) {
	r := redact.New("not-a-recognisable-format-but-secret")
	got := r.RedactText("the value is not-a-recognisable-format-but-secret here")
	if strings.Contains(got, "not-a-recognisable-format-but-secret") {
		t.Fatalf("RedactText did not scrub known value: %q", got)
	}
}

func TestRedactTextPrefersTheLongestOverlappingKnownValue(t *testing.T) {
	r := redact.New("short", "short-but-longer")
	got := r.RedactText("short-but-longer")
	if strings.Contains(got, "short") {
		t.Fatalf("RedactText left a fragment of the longer value: %q", got)
	}
}

func TestRedactJSONScrubsCredentialShapedKeysRegardlessOfValueShape(t *testing.T) {
	r := redact.New()
	raw := json.RawMessage(`{"password":"hunter2","note":"keep this"}`)
	got := r.RedactJSON(raw)

	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["password"] != redact.Placeholder {
		t.Fatalf("password = %v, want %q", decoded["password"], redact.Placeholder)
	}
	if decoded["note"] != "keep this" {
		t.Fatalf("note = %v, want it untouched", decoded["note"])
	}
}

func TestRedactJSONMatchesCredentialKeysButNotLookalikes(t *testing.T) {
	r := redact.New()
	raw := json.RawMessage(`{"access_token":"a","githubToken":"b","client_secret":"c","credentials":"d",` +
		`"max_tokens":"e","input_tokens":"f","secretary":"g","tokenizer":"h"}`)

	var decoded map[string]any
	if err := json.Unmarshal(r.RedactJSON(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"access_token", "githubToken", "client_secret", "credentials"} {
		if decoded[key] != redact.Placeholder {
			t.Errorf("%s = %v, want it redacted", key, decoded[key])
		}
	}
	for _, key := range []string{"max_tokens", "input_tokens", "secretary", "tokenizer"} {
		if decoded[key] == redact.Placeholder {
			t.Errorf("%s was redacted, want it untouched", key)
		}
	}
}

func TestRedactJSONFallsBackToTextForNonJSONContent(t *testing.T) {
	r := redact.New()
	raw := json.RawMessage("plain text with sk-abcdefghijklmnopqrstuvwxyz inside")
	got := r.RedactJSON(raw)
	if !strings.Contains(string(got), redact.Placeholder) {
		t.Fatalf("RedactJSON(non-JSON) = %q, want it redacted as text", got)
	}
}

func TestRedactEventOnlyTouchesFreeTextFields(t *testing.T) {
	r := redact.New("topsecret")
	header := core.EventHeader{ID: "evt-1", TurnID: "turn-1"}

	cases := map[string]core.Event{
		"user message": core.UserMessageSubmitted{
			EventHeader: header,
			Message:     core.Message{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: "my topsecret value"}}},
		},
		"assistant text":         core.AssistantTextStreamed{EventHeader: header, Text: "contains topsecret"},
		"thought":                core.ThoughtStreamed{EventHeader: header, Text: "contains topsecret"},
		"speech":                 core.SpeechRequested{EventHeader: header, Text: "contains topsecret"},
		"tool call":              core.ToolCallStarted{EventHeader: header, Call: core.ToolCall{ID: "c-1", Name: "fs_read", Arguments: json.RawMessage(`{"path":"topsecret"}`)}},
		"tool result":            core.ToolCallFinished{EventHeader: header, Result: core.ToolResult{CallID: "c-1", Name: "fs_read", Content: "topsecret"}, Display: "topsecret"},
		"broker unavailable":     core.BrokerUnavailable{EventHeader: header, Reason: "contains topsecret"},
		"mcp server unavailable": core.MCPServerUnavailable{EventHeader: header, Server: "github", Reason: "contains topsecret"},
	}

	for name, event := range cases {
		t.Run(name, func(t *testing.T) {
			redacted := r.RedactEvent(event)
			blob, err := json.Marshal(redacted)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(blob), "topsecret") {
				t.Fatalf("RedactEvent left the secret in place: %s", blob)
			}
		})
	}
}
