// Package openaicompat adapts an OpenAI-compatible chat-completions endpoint to core.Provider.
// Any endpoint speaking that dialect works, including local servers such as Ollama.
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Kzeezee/travis/internal/core"
)

const providerName = "openai_compatible"

const (
	maxRequestAttempts = 3
	baseRetryDelay     = 250 * time.Millisecond
	// maxRetryDelay caps a server-requested wait: asking for longer fails the turn now rather
	// than stalling an interactive session in silence.
	maxRetryDelay = 10 * time.Second
)

// Provider calls one OpenAI-compatible endpoint. The credential is held here rather than
// resolved per call because the broker that will own secrets does not exist yet; see the
// package README note on the Phase 2 deviation.
type Provider struct {
	baseURL string
	apiKey  string
	client  *http.Client
	clock   core.Clock
}

// BuildProvider returns a provider for the given endpoint. An empty apiKey sends no
// Authorization header, which is what local endpoints expect. A nil client uses
// http.DefaultClient. The clock times the waits between retries.
func BuildProvider(baseURL, apiKey string, client *http.Client, clock core.Clock) *Provider {
	if client == nil {
		client = http.DefaultClient
	}
	return &Provider{baseURL: strings.TrimSuffix(baseURL, "/"), apiKey: apiKey, client: client, clock: clock}
}

type chatRequest struct {
	Model              string         `json:"model"`
	Messages           []chatMessage  `json:"messages"`
	Tools              []chatTool     `json:"tools,omitempty"`
	ToolChoiceBehavior any            `json:"tool_choice,omitempty"`
	Stream             bool           `json:"stream"`
	StreamOptions      *streamOptions `json:"stream_options,omitempty"`
	MaxTokens          int            `json:"max_tokens,omitempty"`
	ReasoningEffort    string         `json:"reasoning_effort,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content,omitempty"` // nil only on a tool-calls-only assistant message
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type chatToolCall struct {
	ID           string               `json:"id"`
	Type         string               `json:"type"`
	Function     chatToolCallFunction `json:"function"`
	ExtraContent json.RawMessage      `json:"extra_content,omitempty"`
}

type chatToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// Chat opens a streaming completion, retrying transient and rate-limit failures that happen
// before the stream starts. Nothing is retried once a stream is returned, so no reply is ever
// emitted twice. The returned stream owns the response body and closes it; callers must
// always close the stream.
func (p *Provider) Chat(ctx context.Context, request core.ProviderRequest) (core.Stream, error) {
	body, err := json.Marshal(chatRequest{
		Model:              request.Model,
		Messages:           buildChatMessages(request.System, request.Messages),
		Tools:              buildChatTools(request.Tools),
		ToolChoiceBehavior: selectToolChoiceBehavior(request.Tools),
		Stream:             true,
		StreamOptions:      &streamOptions{IncludeUsage: true},
		MaxTokens:          request.MaxTokens,
		ReasoningEffort:    selectReasoningEffort(request.ShouldSkipReasoning),
	})
	if err != nil {
		return nil, &core.ProviderError{Provider: providerName, Kind: core.ErrInvalid, Cause: err}
	}

	endpoint, err := url.JoinPath(p.baseURL, "chat", "completions")
	if err != nil {
		return nil, &core.ProviderError{Provider: providerName, Kind: core.ErrInvalid, Cause: err}
	}

	for attempt := 1; ; attempt++ {
		responseBody, retryAfter, err := p.openStream(ctx, endpoint, body)
		if err == nil {
			return buildStream(responseBody), nil
		}
		delay, shouldRetry := selectRetryDelay(err, retryAfter, attempt)
		if !shouldRetry || ctx.Err() != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.clock.After(delay):
		}
	}
}

// selectReasoningEffort leaves the endpoint's own default unless reasoning should be skipped.
func selectReasoningEffort(shouldSkipReasoning bool) string {
	if shouldSkipReasoning {
		return "none"
	}
	return ""
}

type modelListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ListModels reports the endpoint's model IDs, sorted, from GET {baseURL}/models.
func (p *Provider) ListModels(ctx context.Context) ([]string, error) {
	endpoint, err := url.JoinPath(p.baseURL, "models")
	if err != nil {
		return nil, &core.ProviderError{Provider: providerName, Kind: core.ErrInvalid, Cause: err}
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &core.ProviderError{Provider: providerName, Kind: core.ErrInvalid, Cause: err}
	}
	if p.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	response, err := p.client.Do(httpRequest)
	if err != nil {
		return nil, &core.ProviderError{Provider: providerName, Kind: core.ErrTransient, Cause: err}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, translateStatusError(response.StatusCode, response.Body)
	}

	var list modelListResponse
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		return nil, translateMalformedChunk(err)
	}
	ids := make([]string, 0, len(list.Data))
	for _, model := range list.Data {
		// Gemini lists "models/<id>" but accepts the bare id, which is what config and users write.
		if id := strings.TrimPrefix(model.ID, "models/"); id != "" {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// openStream makes one attempt and returns the body of a 200 response. A failed attempt
// also reports the endpoint's Retry-After header, which only the retry decision reads.
func (p *Provider) openStream(ctx context.Context, endpoint string, body []byte) (io.ReadCloser, string, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", &core.ProviderError{Provider: providerName, Kind: core.ErrInvalid, Cause: err}
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	if p.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	// On success the body goes to the caller's stream, which closes it; every failure path
	// below closes it here.
	response, err := p.client.Do(httpRequest)
	if err != nil {
		return nil, "", &core.ProviderError{Provider: providerName, Kind: core.ErrTransient, Cause: err}
	}
	if response.StatusCode != http.StatusOK {
		defer func() { _ = response.Body.Close() }()
		return nil, response.Header.Get("Retry-After"), translateStatusError(response.StatusCode, response.Body)
	}
	return response.Body, "", nil
}

// selectRetryDelay reports how long to wait before the next attempt, or false when the
// failure is not worth retrying. Only transient and rate-limit failures retry (DESIGN.md 5.3).
func selectRetryDelay(err error, retryAfter string, attempt int) (time.Duration, bool) {
	isRetryable := errors.Is(err, core.ErrTransient) || errors.Is(err, core.ErrRateLimit)
	if !isRetryable || attempt >= maxRequestAttempts {
		return 0, false
	}
	// ponytail: Retry-After in seconds only; the HTTP-date form falls back to backoff.
	if seconds, parseErr := strconv.Atoi(strings.TrimSpace(retryAfter)); parseErr == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, seconds <= int(maxRetryDelay/time.Second)
	}
	return rand.N(baseRetryDelay << attempt), true //nolint:gosec // jitter spreads retries; it guards nothing
}

// buildChatMessages converts provider-neutral message content into OpenAI chat messages,
// leading with the system instruction when there is one.
func buildChatMessages(system string, messages []core.Message) []chatMessage {
	chatMessages := make([]chatMessage, 0, 1+len(messages))
	if system != "" {
		chatMessages = append(chatMessages, chatMessage{Role: string(core.RoleSystem), Content: &system})
	}
	for _, message := range messages {
		switch message.Role {
		case core.RoleSystem, core.RoleUser, core.RoleAssistant:
			chatMessages = append(chatMessages, buildAssistantOrUserMessage(message))
		case core.RoleTool:
			chatMessages = append(chatMessages, buildChatMessagesFromToolResults(message.Parts)...)
		default:
			chatMessages = append(chatMessages, buildAssistantOrUserMessage(message))
		}
	}
	return chatMessages
}

func buildAssistantOrUserMessage(message core.Message) chatMessage {
	var content strings.Builder
	toolCalls := make([]chatToolCall, 0)

	for _, part := range message.Parts {
		switch typedPart := part.(type) {
		case core.TextPart:
			content.WriteString(typedPart.Text)
		case core.ToolCallPart:
			toolCalls = append(toolCalls, buildChatToolCall(typedPart.ToolCall))
		}
	}

	wireMessage := chatMessage{Role: string(message.Role), ToolCalls: toolCalls}
	// Endpoints reject a message carrying neither content nor tool_calls, so an empty
	// reply still sends "".
	if text := content.String(); text != "" || len(toolCalls) == 0 {
		wireMessage.Content = &text
	}
	return wireMessage
}

func buildChatMessagesFromToolResults(parts []core.Part) []chatMessage {
	toolResultMessages := make([]chatMessage, 0, len(parts))

	for _, part := range parts {
		toolResultPart, ok := part.(core.ToolResultPart)
		if !ok {
			continue
		}

		resultContent := toolResultPart.ToolResult.Content
		toolResultMessages = append(toolResultMessages, chatMessage{
			Role:       string(core.RoleTool),
			ToolCallID: string(toolResultPart.ToolResult.CallID),
			Content:    &resultContent,
		})
	}

	return toolResultMessages
}

func buildChatToolCall(toolCall core.ToolCall) chatToolCall {
	return chatToolCall{
		ID:   string(toolCall.ID),
		Type: "function",
		Function: chatToolCallFunction{
			Name:      toolCall.Name,
			Arguments: string(toolCall.Arguments),
		},
		ExtraContent: toolCall.ProviderExtra,
	}
}

func buildChatTools(tools []core.ToolSpec) []chatTool {
	if len(tools) == 0 {
		return nil
	}

	chatTools := make([]chatTool, 0, len(tools))
	for _, tool := range tools {
		chatTools = append(chatTools, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}
	return chatTools
}

func selectToolChoiceBehavior(tools []core.ToolSpec) any {
	if len(tools) == 0 {
		return nil
	}
	return "auto"
}

func describeStatus(status int) string {
	return fmt.Sprintf("http %d", status)
}
