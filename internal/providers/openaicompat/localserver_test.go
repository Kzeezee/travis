package openaicompat_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/Kzeezee/travis/internal/providers/openaicompat"
)

func TestDetectOllamaOffersEveryModelItLists(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"models":[{"name":"qwen3:14b"},{"name":"llama3:8b"}]}`)
	})

	got := openaicompat.DetectOllama(context.Background(), server.URL)

	if !got.IsDetected || got.BaseURL != server.URL+"/v1" || !slices.Equal(got.Models, []string{"qwen3:14b", "llama3:8b"}) {
		t.Fatalf("got %+v, want both models in the order Ollama lists them", got)
	}
}

func TestDetectLlamaCppOffersEveryModelItLists(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"gemma-3-4b.gguf"},{"id":"qwen3-8b.gguf"}]}`)
	})

	got := openaicompat.DetectLlamaCpp(context.Background(), server.URL)

	if !got.IsDetected || got.BaseURL != server.URL+"/v1" || !slices.Equal(got.Models, []string{"gemma-3-4b.gguf", "qwen3-8b.gguf"}) {
		t.Fatalf("got %+v, want both models in the order llama.cpp lists them", got)
	}
}

func TestLookUpOllamaContextLengthAsksAboutTheChosenModel(t *testing.T) {
	var asked []string
	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Model string }
		if r.URL.Path != "/api/show" || json.NewDecoder(r.Body).Decode(&request) != nil {
			http.NotFound(w, r)
			return
		}
		asked = append(asked, request.Model)
		switch request.Model {
		case "qwen3:14b":
			_, _ = io.WriteString(w, `{"parameters":"stop \"<|im_end|>\"\nnum_ctx                        40960","model_info":{"general.architecture":"qwen3","qwen3.context_length":131072}}`)
		case "llama3:8b":
			_, _ = io.WriteString(w, `{"parameters":"num_ctx 8192"}`)
		default:
			http.Error(w, "unknown model", http.StatusNotFound)
		}
	})

	for model, want := range map[string]int{"llama3:8b": 8192, "qwen3:14b": 40960, "missing": 0} {
		if got := openaicompat.LookUpOllamaContextLength(context.Background(), server.URL, model); got != want {
			t.Errorf("%s: got %d, want %d", model, got, want)
		}
	}
	if len(asked) != 3 {
		t.Fatalf("asked about %v, want one lookup per chosen model", asked)
	}
}

func TestLookUpLlamaCppContextLengthReadsTheServerWideWindow(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/props" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"default_generation_settings":{"n_ctx":8192}}`)
	})

	if got := openaicompat.LookUpLlamaCppContextLength(context.Background(), server.URL, "gemma-3-4b.gguf"); got != 8192 {
		t.Fatalf("got %d, want 8192", got)
	}
}

// Context length is best effort: a server that will not say leaves it at 0.
func TestLookUpContextLengthIsZeroWhenTheServerDoesNotSay(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) })

	for name, got := range map[string]int{
		"ollama":    openaicompat.LookUpOllamaContextLength(context.Background(), server.URL, "qwen3:14b"),
		"llama.cpp": openaicompat.LookUpLlamaCppContextLength(context.Background(), server.URL, "gemma"),
	} {
		if got != 0 {
			t.Errorf("%s: got %d, want 0", name, got)
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

func TestLookUpOllamaContextLengthNeverUsesTheTrainedContextLength(t *testing.T) {
	server := startEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			_, _ = io.WriteString(w, `{"parameters":"stop \"<|eot_id|>\"","model_info":{"general.architecture":"llama","llama.context_length":131072}}`)
		}
	})
	if got := openaicompat.LookUpOllamaContextLength(context.Background(), server.URL, "llama3.1:8b"); got != 0 {
		t.Fatalf("got %d; Ollama runs with a smaller window and truncates silently, so leave it unknown", got)
	}
}
