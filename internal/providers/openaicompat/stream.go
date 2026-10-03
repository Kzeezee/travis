package openaicompat

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const doneMarker = "[DONE]"

// stream turns server-sent events into core.Chunk values. Each frame is validated here,
// at the boundary that receives it, so nothing above this package parses provider JSON.
type stream struct {
	body             io.ReadCloser
	reader           *bufio.Reader
	finished         bool
	pendingToolCalls map[int]*pendingToolCall
}

// pendingToolCall accumulates one call's deltas, which arrive split across frames by index.
type pendingToolCall struct {
	id        core.ID
	name      string
	arguments strings.Builder
}

func buildStream(body io.ReadCloser) *stream {
	return &stream{body: body, reader: bufio.NewReader(body), pendingToolCalls: make(map[int]*pendingToolCall)}
}

// streamChunk mirrors one OpenAI-compatible SSE frame and is not trusted until decoded.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string                `json:"content"`
			ToolCalls []streamToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type streamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Next returns the next chunk, or io.EOF once the endpoint signals the end of the stream.
func (s *stream) Next() (core.Chunk, error) {
	if s.finished {
		return core.Chunk{}, io.EOF
	}

	for {
		line, err := s.reader.ReadString('\n')
		if payload, ok := readDataPayload(line); ok {
			if payload == doneMarker {
				s.finished = true
				return core.Chunk{}, io.EOF
			}
			return s.decodeChunk(payload)
		}
		if err != nil {
			s.finished = true
			if errors.Is(err, io.EOF) {
				return core.Chunk{}, io.EOF
			}
			return core.Chunk{}, translateTransportError(err)
		}
	}
}

// Close releases the response body.
func (s *stream) Close() error {
	return s.body.Close()
}

// readDataPayload reports the content of an SSE data line, skipping blank lines,
// comments, and other fields.
func readDataPayload(line string) (string, bool) {
	trimmed := strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(trimmed, "data:") {
		return "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	return payload, payload != ""
}

func (s *stream) decodeChunk(payload string) (core.Chunk, error) {
	var raw streamChunk
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return core.Chunk{}, translateMalformedChunk(err)
	}

	var chunk core.Chunk
	if len(raw.Choices) > 0 {
		choice := raw.Choices[0]
		chunk.Text = choice.Delta.Content
		for _, delta := range choice.Delta.ToolCalls {
			s.addToolCallDelta(delta)
		}
		if choice.FinishReason != nil {
			chunk.Finish = selectStopReason(*choice.FinishReason)
			chunk.ToolCalls = s.takeToolCalls()
		}
	}
	if raw.Usage != nil {
		chunk.Usage = &core.Usage{
			InputTokens:  raw.Usage.PromptTokens,
			OutputTokens: raw.Usage.CompletionTokens,
		}
	}
	return chunk, nil
}

func (s *stream) addToolCallDelta(delta streamToolCallDelta) {
	call, isPending := s.pendingToolCalls[delta.Index]
	if !isPending {
		call = &pendingToolCall{}
		s.pendingToolCalls[delta.Index] = call
	}
	if delta.ID != "" {
		call.id = core.ID(delta.ID)
	}
	if delta.Function.Name != "" {
		call.name = delta.Function.Name
	}
	call.arguments.WriteString(delta.Function.Arguments)
}

// takeToolCalls returns the assembled calls in index order and resets for the next reply.
// Arguments are not checked for valid JSON: the tool's strict decode turns bad input into an
// invalid_arguments result the model can retry, rather than a failed stream.
func (s *stream) takeToolCalls() []core.ToolCall {
	if len(s.pendingToolCalls) == 0 {
		return nil
	}
	calls := make([]core.ToolCall, 0, len(s.pendingToolCalls))
	for _, index := range slices.Sorted(maps.Keys(s.pendingToolCalls)) {
		pending := s.pendingToolCalls[index]
		arguments := pending.arguments.String()
		if arguments == "" {
			arguments = "{}"
		}
		calls = append(calls, core.ToolCall{ID: pending.id, Name: pending.name, Arguments: json.RawMessage(arguments)})
	}
	clear(s.pendingToolCalls)
	return calls
}

func selectStopReason(reason string) core.StopReason {
	switch reason {
	case "length":
		return core.StopLength
	case "tool_calls":
		return core.StopToolCalls
	default:
		return core.StopEndTurn
	}
}
