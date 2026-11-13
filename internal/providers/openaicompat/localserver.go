package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Kzeezee/travis/internal/core"
)

// localProbeTimeout bounds each request to a server on this machine: one that is running answers
// well within it, and the setup wizard should not wait on one that is not.
const localProbeTimeout = 300 * time.Millisecond

// LocalServer is what a probe of one model server on this machine found.
type LocalServer struct {
	// BaseURL is the server's OpenAI-compatible endpoint, the one a provider profile points at.
	BaseURL string
	// Models is every model the server offers, in the order it lists them; empty when it offers none.
	Models     []string
	IsDetected bool
}

// DetectOllama probes the Ollama server at origin, such as http://127.0.0.1:11434.
func DetectOllama(ctx context.Context, origin string) LocalServer {
	origin = strings.TrimSuffix(origin, "/")
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := exchangeLocalJSON(ctx, http.MethodGet, origin+"/api/tags", nil, &tags); err != nil {
		return LocalServer{}
	}
	server := LocalServer{BaseURL: origin + "/v1", IsDetected: true}
	for _, model := range tags.Models {
		server.Models = append(server.Models, model.Name)
	}
	return server
}

// LookUpOllamaContextLength reports model's context window in tokens at the Ollama server at origin; 0 when unknown.
func LookUpOllamaContextLength(ctx context.Context, origin, model string) int {
	// Only an explicit num_ctx is the window Ollama runs with; model_info's context_length is what the model was
	// trained on, and Ollama silently drops the start of a longer prompt, so pre-filling it would hide truncation (D-33).
	var show struct {
		Parameters string `json:"parameters"`
	}
	if exchangeLocalJSON(ctx, http.MethodPost, strings.TrimSuffix(origin, "/")+"/api/show", map[string]string{"model": model}, &show) != nil {
		return 0
	}
	return parseOllamaNumCtx(show.Parameters)
}

// parseOllamaNumCtx reads "num_ctx <n>" from a Modelfile's parameter lines; 0 when it is not set.
func parseOllamaNumCtx(parameters string) int {
	for line := range strings.Lines(parameters) {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "num_ctx" {
			if n, err := strconv.Atoi(fields[1]); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// DetectLlamaCpp probes the llama.cpp server at origin, such as http://127.0.0.1:8080.
func DetectLlamaCpp(ctx context.Context, origin string) LocalServer {
	origin = strings.TrimSuffix(origin, "/")
	probeCtx, cancel := context.WithTimeout(ctx, localProbeTimeout)
	defer cancel()
	models, err := BuildProvider(origin+"/v1", "", nil, core.SystemClock{}).ListModels(probeCtx)
	if err != nil {
		return LocalServer{}
	}
	return LocalServer{BaseURL: origin + "/v1", Models: models, IsDetected: true}
}

// LookUpLlamaCppContextLength reports the server-wide context window in tokens at the llama.cpp server at origin; 0 when unknown.
func LookUpLlamaCppContextLength(ctx context.Context, origin, _ string) int {
	var props struct {
		DefaultGenerationSettings struct {
			ContextLength int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if exchangeLocalJSON(ctx, http.MethodGet, strings.TrimSuffix(origin, "/")+"/props", nil, &props) != nil {
		return 0
	}
	return props.DefaultGenerationSettings.ContextLength
}

// exchangeLocalJSON sends request, when not nil, as JSON and decodes a 200 reply into reply.
func exchangeLocalJSON(ctx context.Context, method, endpoint string, request, reply any) error {
	ctx, cancel := context.WithTimeout(ctx, localProbeTimeout)
	defer cancel()
	var body bytes.Buffer
	if request != nil {
		if err := json.NewEncoder(&body).Encode(request); err != nil {
			return err
		}
	}
	httpRequest, err := http.NewRequestWithContext(ctx, method, endpoint, &body)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", endpoint, describeStatus(response.StatusCode))
	}
	return json.NewDecoder(response.Body).Decode(reply)
}
