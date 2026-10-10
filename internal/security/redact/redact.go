// Package redact scrubs secrets from event payloads before they reach the bus or disk
// (§5.12.3). It is pure and holds no IO: by value (secrets already resolved elsewhere,
// e.g. the provider API key) and by pattern (credential-shaped key names and known
// secret value formats).
package redact

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

// Placeholder replaces every redacted value.
const Placeholder = "[REDACTED]"

// keyPattern matches a JSON object key whose value is credential-shaped regardless of
// the value's own format.
var keyPattern = regexp.MustCompile(`(?i)(api[_-]?key|secret|passwd|password|token|authorization|bearer|credential|private[_-]?key|access[_-]?key)`)

// valuePatterns catches known secret formats in free text, independent of any key name.
var valuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),                                                     // OpenAI/Anthropic-style API keys
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                                          // AWS access key ID
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),                                                // GitHub tokens
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),                                              // Slack tokens
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._-]{10,}`),                                          // Authorization: Bearer <token>
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),                         // JWT
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), // PEM private key
}

// Redactor scrubs known secret values and credential-shaped fields from event payloads.
type Redactor struct {
	knownValues []string
}

// New returns a Redactor that also scrubs each non-empty known value verbatim, longest
// first so a short value is never left exposed inside a longer one it is a substring of.
func New(knownValues ...string) Redactor {
	var nonEmpty []string
	for _, value := range knownValues {
		if value != "" {
			nonEmpty = append(nonEmpty, value)
		}
	}
	sort.Slice(nonEmpty, func(i, j int) bool { return len(nonEmpty[i]) > len(nonEmpty[j]) })
	return Redactor{knownValues: nonEmpty}
}

// RedactText scrubs known values and known secret-value formats from free text.
func (r Redactor) RedactText(text string) string {
	for _, value := range r.knownValues {
		text = strings.ReplaceAll(text, value, Placeholder)
	}
	for _, pattern := range valuePatterns {
		text = pattern.ReplaceAllString(text, Placeholder)
	}
	return text
}

// RedactJSON scrubs raw, possibly-non-JSON bytes: valid JSON is walked so a
// credential-shaped key redacts its value regardless of shape; anything else falls back
// to RedactText.
func (r Redactor) RedactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return json.RawMessage(r.RedactText(string(raw)))
	}
	redacted, err := json.Marshal(r.redactValue(decoded))
	if err != nil {
		return json.RawMessage(r.RedactText(string(raw)))
	}
	return redacted
}

func (r Redactor) redactValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, val := range v {
			if keyPattern.MatchString(key) {
				result[key] = Placeholder
				continue
			}
			result[key] = r.redactValue(val)
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = r.redactValue(item)
		}
		return result
	case string:
		return r.RedactText(v)
	default:
		return v
	}
}

// RedactEvent returns a copy of event with every free-text or argument/result field
// scrubbed. Fields that cannot carry a secret (ids, timestamps, status flags) are left
// untouched.
func (r Redactor) RedactEvent(event core.Event) core.Event {
	switch e := event.(type) {
	case core.UserMessageSubmitted:
		e.Message = r.redactMessage(e.Message)
		return e
	case core.AssistantTextStreamed:
		e.Text = r.RedactText(e.Text)
		return e
	case core.ThoughtStreamed:
		e.Text = r.RedactText(e.Text)
		return e
	case core.ToolCallStarted:
		e.Call.Arguments = r.RedactJSON(e.Call.Arguments)
		return e
	case core.ToolCallFinished:
		e.Result.Content = r.RedactText(e.Result.Content)
		e.Display = r.RedactText(e.Display)
		return e
	case core.TurnCompleted:
		return e
	default:
		return event
	}
}

func (r Redactor) redactMessage(message core.Message) core.Message {
	parts := make([]core.Part, len(message.Parts))
	for i, part := range message.Parts {
		switch p := part.(type) {
		case core.TextPart:
			p.Text = r.RedactText(p.Text)
			parts[i] = p
		case core.ToolCallPart:
			p.ToolCall.Arguments = r.RedactJSON(p.ToolCall.Arguments)
			parts[i] = p
		case core.ToolResultPart:
			p.ToolResult.Content = r.RedactText(p.ToolResult.Content)
			parts[i] = p
		default:
			parts[i] = part
		}
	}
	message.Parts = parts
	return message
}
