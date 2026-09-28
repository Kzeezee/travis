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
	Model              string         `json:"model"`
	Messages           []chatMessage  `json:"messages"`
	Tools              []chatTool     `json:"tools,omitempty"`
	ToolChoiceBehavior any            `json:"tool_choice,omitempty"`
	Stream             bool           `json:"stream"`
	StreamOptions      *streamOptions `json:"stream_options,omitempty"`
	MaxTokens          int            `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
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
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function chatToolCallFunction `json:"function"`
}

type chatToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// Chat opens a streaming completion. The returned stream owns the response body and
// closes it; callers must always close the stream.
func (p *Provider) Chat(ctx context.Context, request core.ProviderRequest) (core.Stream, error) {
	body, err := json.Marshal(chatRequest{
		Model:              request.Model,
		Messages:           buildChatMessages(request.Messages),
		Tools:              buildChatTools(request.Tools),
		ToolChoiceBehavior: selectToolChoiceBehavior(request.Tools),
		Stream:             true,
		StreamOptions:      &streamOptions{IncludeUsage: true},
		MaxTokens:          request.MaxTokens,
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

// buildChatMessages converts provider-neutral message content into OpenAI chat messages.
func buildChatMessages(messages []core.Message) []chatMessage {
	chatMessages := make([]chatMessage, 0, len(messages))
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

	return chatMessage{
		Role:      string(message.Role),
		Content:   content.String(),
		ToolCalls: toolCalls,
	}
}

func buildChatMessagesFromToolResults(parts []core.Part) []chatMessage {
	toolResultMessages := make([]chatMessage, 0, len(parts))

	for _, part := range parts {
		toolResultPart, ok := part.(core.ToolResultPart)
		if !ok {
			continue
		}

		toolResultMessages = append(toolResultMessages, chatMessage{
			Role:       string(core.RoleTool),
			ToolCallID: string(toolResultPart.ToolResult.CallID),
			Content:    toolResultPart.ToolResult.Content,
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
