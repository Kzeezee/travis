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
	var gotAuth, gotPath string
	var gotBody map[string]any

	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
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

	if gotAuth != "Bearer "+testAPIKey {
		t.Fatalf("got Authorization %q, want a bearer credential", gotAuth)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("got path %q, want /v1/chat/completions", gotPath)
	}
	if gotBody["model"] != "gpt-5" {
		t.Fatalf("got model %v, want gpt-5", gotBody["model"])
	}
	if gotBody["stream"] != true {
		t.Fatalf("got stream %v, want true", gotBody["stream"])
	}
	messages, ok := gotBody["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("got messages %v, want one message", gotBody["messages"])
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "hi" {
		t.Fatalf("got message %v, want the user prompt", first)
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

func TestStreamReportsAMalformedFrameAsInvalid(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
		writeStream(w, `{"choices":[`)
	})

	_, err := drain(t, openChat(t, server, ""))
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("got %v, want core.ErrInvalid", err)
	}
}

func TestChatTranslatesStatusCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"rate limited", http.StatusTooManyRequests, core.ErrExhausted},
		{"bad request", http.StatusBadRequest, core.ErrInvalid},
		{"unauthorized", http.StatusUnauthorized, core.ErrInvalid},
		{"server failure", http.StatusInternalServerError, core.ErrTransient},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := startEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = io.WriteString(w, `{"error":{"message":"nope"}}`)
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
