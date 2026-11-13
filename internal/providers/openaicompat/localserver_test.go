package openaicompat_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Kzeezee/travis/internal/providers/openaicompat"
)

func TestDetectOllamaOffersItsFirstModelWithItsContextLength(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"qwen3:14b"},{"name":"llama3:8b"}]}`)
		case "POST /api/show":
			var request struct{ Model string }
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != "qwen3:14b" {
				http.Error(w, "unknown model", http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, `{"parameters":"stop \"<|im_end|>\"\nnum_ctx                        40960","model_info":{"general.architecture":"qwen3","qwen3.context_length":131072}}`)
		default:
			http.NotFound(w, r)
		}
	})

	got := openaicompat.DetectOllama(context.Background(), server.URL)

	want := openaicompat.LocalServer{BaseURL: server.URL + "/v1", Model: "qwen3:14b", ContextLength: 40960, IsDetected: true}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDetectLlamaCppOffersItsLoadedModelWithItsContextLength(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"gemma-3-4b.gguf"}]}`)
		case "/props":
			_, _ = io.WriteString(w, `{"default_generation_settings":{"n_ctx":8192}}`)
		default:
			http.NotFound(w, r)
		}
	})

	got := openaicompat.DetectLlamaCpp(context.Background(), server.URL)

	want := openaicompat.LocalServer{BaseURL: server.URL + "/v1", Model: "gemma-3-4b.gguf", ContextLength: 8192, IsDetected: true}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// Context length is best effort: a server that will not say leaves it at 0, still detected.
func TestDetectLocalServerLeavesContextLengthUnknownWhenTheServerDoesNotSay(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"qwen3:14b"}]}`)
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"gemma"}]}`)
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	})

	for name, detect := range map[string]func(context.Context, string) openaicompat.LocalServer{
		"ollama": openaicompat.DetectOllama, "llama.cpp": openaicompat.DetectLlamaCpp,
	} {
		if got := detect(context.Background(), server.URL); !got.IsDetected || got.Model == "" || got.ContextLength != 0 {
			t.Errorf("%s: got %+v, want detected with a model and context length 0", name, got)
		}
	}
}

func TestDetectLocalServerReportsAServerThatDoesNotAnswer(t *testing.T) {
	server := startEndpoint(t, http.NotFound)
	closed := startEndpoint(t, http.NotFound)
	closed.Close()

	for _, origin := range []string{server.URL, closed.URL} {
		if got := openaicompat.DetectOllama(context.Background(), origin); got.IsDetected {
			t.Errorf("ollama at %s: got %+v, want not detected", origin, got)
		}
		if got := openaicompat.DetectLlamaCpp(context.Background(), origin); got.IsDetected {
			t.Errorf("llama.cpp at %s: got %+v, want not detected", origin, got)
		}
	}
}

func TestDetectOllamaNeverPrefillsTheTrainedContextLength(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"llama3.1:8b"}]}`)
		case "/api/show":
			_, _ = io.WriteString(w, `{"parameters":"stop \"<|eot_id|>\"","model_info":{"general.architecture":"llama","llama.context_length":131072}}`)
		}
	})
	if got := openaicompat.DetectOllama(context.Background(), server.URL); got.ContextLength != 0 {
		t.Fatalf("got %d; Ollama runs with a smaller window and truncates silently, so leave it unknown", got.ContextLength)
	}
}
