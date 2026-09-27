package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/providers/openai"
)

const testAPIKey = "sk-test-secret-value"

func startEndpoint(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func writeStream(w http.ResponseWriter, frames ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, frame := range frames {
		_, _ = io.WriteString(w, "data: "+frame+"\n\n")
	}
}

func drain(t *testing.T, stream core.Stream) ([]core.Chunk, error) {
	t.Helper()
	var chunks []core.Chunk
	for {
		chunk, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return chunks, nil
		}
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, chunk)
	}
}

func TestChatSendsAStreamingRequestWithTheCredential(t *testing.T) {
	var capturedAuthorizationHeader, capturedRequestPath string
	var capturedRequestBody map[string]any

	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		capturedAuthorizationHeader = r.Header.Get("Authorization")
		capturedRequestPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&capturedRequestBody)
		writeStream(w, `{"choices":[{"delta":{"content":"ok"}}]}`, "[DONE]")
	})

	provider := openai.BuildProvider(server.URL+"/v1", testAPIKey, server.Client())
	stream, err := provider.Chat(context.Background(), core.ProviderRequest{
		Model:    "gpt-5",
		Messages: []core.Message{{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Chat returned %v, want nil", err)
	}
	defer func() { _ = stream.Close() }()

	if capturedAuthorizationHeader != "Bearer "+testAPIKey {
		t.Fatalf("got Authorization %q, want a bearer credential", capturedAuthorizationHeader)
	}
	if capturedRequestPath != "/v1/chat/completions" {
		t.Fatalf("got path %q, want /v1/chat/completions", capturedRequestPath)
	}
	if capturedRequestBody["model"] != "gpt-5" {
		t.Fatalf("got model %v, want gpt-5", capturedRequestBody["model"])
	}
	if capturedRequestBody["stream"] != true {
		t.Fatalf("got stream %v, want true", capturedRequestBody["stream"])
	}
	messages, ok := capturedRequestBody["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("got messages %v, want one message", capturedRequestBody["messages"])
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "hi" {
		t.Fatalf("got message %v, want the user prompt", first)
	}
}

func TestChatSerializesTools(t *testing.T) {
	var capturedRequestBody map[string]any
	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedRequestBody)
		writeStream(w, "[DONE]")
	})

	provider := openai.BuildProvider(server.URL+"/v1", testAPIKey, server.Client())
	stream, err := provider.Chat(context.Background(), core.ProviderRequest{
		Model: "gpt-5",
		Tools: []core.ToolSpec{{
			Name:        "fs.read",
			Description: "Read a file",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{"path":{"type":"string"}},
				"required":["path"]
			}`),
		}},
	})
	if err != nil {
		t.Fatalf("Chat returned %v, want nil", err)
	}
	defer func() { _ = stream.Close() }()

	tools, ok := capturedRequestBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("got tools %v, want one tool", capturedRequestBody["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Fatalf("got tool type %v, want function", tool["type"])
	}
	function, _ := tool["function"].(map[string]any)
	if function["name"] != "fs.read" || function["description"] != "Read a file" {
		t.Fatalf("got function %v, want fs.read with description", function)
	}
	parameters, _ := function["parameters"].(map[string]any)
	if parameters["type"] != "object" {
		t.Fatalf("got parameters %v, want object schema", parameters)
	}
	if capturedRequestBody["tool_choice"] != "auto" {
		t.Fatalf("got tool_choice %v, want auto", capturedRequestBody["tool_choice"])
	}
}

func TestChatSerializesToolCallAndToolResultMessages(t *testing.T) {
	var capturedRequestBody map[string]any
	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedRequestBody)
		writeStream(w, "[DONE]")
	})

	toolCallArguments := json.RawMessage(`{"path":"README.md"}`)
	provider := openai.BuildProvider(server.URL+"/v1", testAPIKey, server.Client())
	stream, err := provider.Chat(context.Background(), core.ProviderRequest{
		Model: "gpt-5",
		Messages: []core.Message{
			{
				Role: core.RoleAssistant,
				Parts: []core.Part{
					core.TextPart{Text: "reading file"},
					core.ToolCallPart{ToolCall: core.ToolCall{
						ID:        core.ID("call_123"),
						Name:      "fs.read",
						Arguments: toolCallArguments,
					}},
				},
			},
			{
				Role: core.RoleTool,
				Parts: []core.Part{
					core.ToolResultPart{ToolResult: core.ToolResult{
						CallID:  core.ID("call_123"),
						Name:    "fs.read",
						Content: "file contents",
					}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Chat returned %v, want nil", err)
	}
	defer func() { _ = stream.Close() }()

	messages, ok := capturedRequestBody["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("got messages %v, want assistant and tool messages", capturedRequestBody["messages"])
	}

	assistantMessage, _ := messages[0].(map[string]any)
	if assistantMessage["role"] != "assistant" || assistantMessage["content"] != "reading file" {
		t.Fatalf("got assistant message %v, want assistant text", assistantMessage)
	}
	toolCalls, ok := assistantMessage["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("got tool_calls %v, want one tool call", assistantMessage["tool_calls"])
	}
	toolCall, _ := toolCalls[0].(map[string]any)
	if toolCall["id"] != "call_123" || toolCall["type"] != "function" {
		t.Fatalf("got tool call %v, want call_123 function", toolCall)
	}
	function, _ := toolCall["function"].(map[string]any)
	if function["name"] != "fs.read" || function["arguments"] != string(toolCallArguments) {
		t.Fatalf("got tool call function %v, want fs.read arguments", function)
	}

	toolMessage, _ := messages[1].(map[string]any)
	if toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "call_123" || toolMessage["content"] != "file contents" {
		t.Fatalf("got tool message %v, want tool result content", toolMessage)
	}
}

func TestChatOmitsTheCredentialWhenNoKeyIsConfigured(t *testing.T) {
	var gotAuth string
	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		writeStream(w, "[DONE]")
	})

	provider := openai.BuildProvider(server.URL+"/v1", "", server.Client())
	stream, err := provider.Chat(context.Background(), core.ProviderRequest{Model: "qwen3:14b"})
	if err != nil {
		t.Fatalf("Chat returned %v, want nil", err)
	}
	defer func() { _ = stream.Close() }()

	if gotAuth != "" {
		t.Fatalf("got Authorization %q, want none for a keyless endpoint", gotAuth)
	}
}

func TestStreamDecodesTextFinishReasonAndUsage(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
		writeStream(w,
			`{"choices":[{"delta":{"content":"hel"},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"content":"lo"},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`,
			"[DONE]",
		)
	})

	stream := openChat(t, server, "")
	chunks, err := drain(t, stream)
	if err != nil {
		t.Fatalf("draining returned %v, want nil", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}
	if chunks[0].Text+chunks[1].Text != "hello" {
		t.Fatalf("got text %q+%q, want hello", chunks[0].Text, chunks[1].Text)
	}
	final := chunks[2]
	if final.Finish != core.StopEndTurn {
		t.Fatalf("got finish %q, want %q", final.Finish, core.StopEndTurn)
	}
	if final.Usage == nil || final.Usage.InputTokens != 7 || final.Usage.OutputTokens != 2 {
		t.Fatalf("got usage %+v, want 7 in / 2 out", final.Usage)
	}
}

func TestStreamAssemblesToolCallsOnTheFinishFrame(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
		writeStream(w,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_123","type":"function","function":{"name":"fs.read","arguments":""}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\""}},{"index":1,"id":"call_456","type":"function","function":{"name":"clock.now"}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"README.md\"}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			"[DONE]",
		)
	})

	chunks, err := drain(t, openChat(t, server, ""))
	if err != nil {
		t.Fatalf("draining returned %v, want nil", err)
	}
	if len(chunks) != 4 {
		t.Fatalf("got %d chunks, want 4", len(chunks))
	}
	for _, chunk := range chunks[:3] {
		if len(chunk.ToolCalls) != 0 {
			t.Fatalf("got tool calls %+v before the finish frame, want none", chunk.ToolCalls)
		}
	}

	final := chunks[3]
	if final.Finish != core.StopToolCalls {
		t.Fatalf("got finish %q, want %q", final.Finish, core.StopToolCalls)
	}
	want := []core.ToolCall{
		{ID: "call_123", Name: "fs.read", Arguments: json.RawMessage(`{"path":"README.md"}`)},
		{ID: "call_456", Name: "clock.now", Arguments: json.RawMessage(`{}`)},
	}
	if len(final.ToolCalls) != len(want) {
		t.Fatalf("got tool calls %+v, want %+v", final.ToolCalls, want)
	}
	for i := range want {
		got := final.ToolCalls[i]
		if got.ID != want[i].ID || got.Name != want[i].Name || string(got.Arguments) != string(want[i].Arguments) {
			t.Fatalf("got tool call %d %+v, want %+v", i, got, want[i])
		}
	}
}

func TestStreamEndsCleanlyWithoutADoneMarker(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
		writeStream(w, `{"choices":[{"delta":{"content":"only"}}]}`)
	})

	chunks, err := drain(t, openChat(t, server, ""))
	if err != nil {
		t.Fatalf("draining returned %v, want nil", err)
	}
	if len(chunks) != 1 || chunks[0].Text != "only" {
		t.Fatalf("got %v, want one chunk of text", chunks)
	}
}

func TestStreamReportsAMalformedFrame(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
		writeStream(w, `{"choices":[`)
	})

	_, err := drain(t, openChat(t, server, ""))
	if !errors.Is(err, core.ErrProviderMalformed) {
		t.Fatalf("got %v, want core.ErrProviderMalformed", err)
	}
}

func TestChatTranslatesStatusCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, want: core.ErrRateLimit},
		{name: "bad request", status: http.StatusBadRequest, want: core.ErrInvalid},
		{name: "unauthorized", status: http.StatusUnauthorized, want: core.ErrAuth},
		{name: "forbidden", status: http.StatusForbidden, want: core.ErrAuth},
		{name: "request too large", status: http.StatusRequestEntityTooLarge, want: core.ErrContextLength},
		{
			name:   "context size from openai error body",
			status: http.StatusBadRequest,
			body:   `{"error":{"code":"context_length_exceeded","message":"too many tokens"}}`,
			want:   core.ErrContextLength,
		},
		{name: "server failure", status: http.StatusInternalServerError, want: core.ErrTransient},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := startEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				errorBody := testCase.body
				if errorBody == "" {
					errorBody = `{"error":{"message":"nope"}}`
				}
				_, _ = io.WriteString(w, errorBody)
			})

			provider := openai.BuildProvider(server.URL+"/v1", testAPIKey, server.Client())
			_, err := provider.Chat(context.Background(), core.ProviderRequest{Model: "gpt-5"})

			if !errors.Is(err, testCase.want) {
				t.Fatalf("got %v, want %v", err, testCase.want)
			}
			var providerErr *core.ProviderError
			if !errors.As(err, &providerErr) {
				t.Fatalf("got %T, want a *core.ProviderError", err)
			}
			if strings.Contains(err.Error(), testAPIKey) {
				t.Fatalf("error text leaked the credential: %q", err.Error())
			}
		})
	}
}

func openChat(t *testing.T, server *httptest.Server, apiKey string) core.Stream {
	t.Helper()
	provider := openai.BuildProvider(server.URL+"/v1", apiKey, server.Client())
	stream, err := provider.Chat(context.Background(), core.ProviderRequest{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("Chat returned %v, want nil", err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	return stream
}
