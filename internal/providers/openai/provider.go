// Package openai adapts an OpenAI-compatible chat-completions endpoint to core.Provider.
// Any endpoint speaking that dialect works, including local servers such as Ollama.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const providerName = "openai"

// Provider calls one OpenAI-compatible endpoint. The credential is held here rather than
// resolved per call because the broker that will own secrets does not exist yet; see the
// package README note on the Phase 2 deviation.
type Provider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// BuildProvider returns a provider for the given endpoint. An empty apiKey sends no
// Authorization header, which is what local endpoints expect. A nil client uses
// http.DefaultClient.
func BuildProvider(baseURL, apiKey string, client *http.Client) *Provider {
	if client == nil {
		client = http.DefaultClient
	}
	return &Provider{baseURL: strings.TrimSuffix(baseURL, "/"), apiKey: apiKey, client: client}
}

type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []chatMessage  `json:"messages"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// Chat opens a streaming completion. The returned stream owns the response body and
// closes it; callers must always close the stream.
func (p *Provider) Chat(ctx context.Context, request core.ProviderRequest) (core.Stream, error) {
	body, err := json.Marshal(chatRequest{
		Model:         request.Model,
		Messages:      buildChatMessages(request.Messages),
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
		MaxTokens:     request.MaxTokens,
	})
	if err != nil {
		return nil, &core.ProviderError{Provider: providerName, Kind: core.ErrInvalid, Cause: err}
	}

	endpoint, err := url.JoinPath(p.baseURL, "chat", "completions")
	if err != nil {
		return nil, &core.ProviderError{Provider: providerName, Kind: core.ErrInvalid, Cause: err}
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &core.ProviderError{Provider: providerName, Kind: core.ErrInvalid, Cause: err}
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	if p.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	// On success the body is handed to the stream, which closes it; every failure path
	// below closes it here.
	response, err := p.client.Do(httpRequest)
	if err != nil {
		return nil, &core.ProviderError{Provider: providerName, Kind: core.ErrTransient, Cause: err}
	}
	if response.StatusCode != http.StatusOK {
		defer func() { _ = response.Body.Close() }()
		return nil, translateStatusError(response.StatusCode, response.Body)
	}

	return buildStream(response.Body), nil
}

// buildChatMessages flattens provider-neutral parts into the wire's single content
// string. Only text survives, which is all 1A sends: tool parts arrive with the tool
// registry.
func buildChatMessages(messages []core.Message) []chatMessage {
	converted := make([]chatMessage, 0, len(messages))
	for _, message := range messages {
		var content strings.Builder
		for _, part := range message.Parts {
			if text, ok := part.(core.TextPart); ok {
				content.WriteString(text.Text)
			}
		}
		converted = append(converted, chatMessage{Role: string(message.Role), Content: content.String()})
	}
	return converted
}

func describeStatus(status int) string {
	return fmt.Sprintf("http %d", status)
}
