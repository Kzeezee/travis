package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	// Model is the first model the server offers; empty when it offers none.
	Model string
	// ContextLength is Model's context window in tokens, pre-filled for config (D-33); 0 when the server does not say.
	ContextLength int
	IsDetected    bool
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
	if len(tags.Models) == 0 {
		return server
	}
	server.Model = tags.Models[0].Name
	var show struct {
		ModelInfo map[string]any `json:"model_info"`
	}
	if exchangeLocalJSON(ctx, http.MethodPost, origin+"/api/show", map[string]string{"model": server.Model}, &show) == nil {
		architecture, _ := show.ModelInfo["general.architecture"].(string)
		contextLength, _ := show.ModelInfo[architecture+".context_length"].(float64)
		server.ContextLength = int(contextLength)
	}
	return server
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
	server := LocalServer{BaseURL: origin + "/v1", IsDetected: true}
	if len(models) == 0 {
		return server
	}
	server.Model = models[0]
	var props struct {
		DefaultGenerationSettings struct {
			ContextLength int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if exchangeLocalJSON(ctx, http.MethodGet, origin+"/props", nil, &props) == nil {
		server.ContextLength = props.DefaultGenerationSettings.ContextLength
	}
	return server
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
