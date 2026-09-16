package openai

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const doneMarker = "[DONE]"

// stream turns server-sent events into core.Chunk values. Each frame is validated here,
// at the boundary that receives it, so nothing above this package parses provider JSON.
type stream struct {
	body     io.ReadCloser
	reader   *bufio.Reader
	finished bool
}

func buildStream(body io.ReadCloser) *stream {
	return &stream{body: body, reader: bufio.NewReader(body)}
}

// streamChunk mirrors one OpenAI-compatible SSE frame and is not trusted until decoded.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
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
			return decodeChunk(payload)
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

func decodeChunk(payload string) (core.Chunk, error) {
	var raw streamChunk
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return core.Chunk{}, translateMalformedChunk(err)
	}

	var chunk core.Chunk
	if len(raw.Choices) > 0 {
		chunk.Text = raw.Choices[0].Delta.Content
		if raw.Choices[0].FinishReason != nil {
			chunk.Finish = selectStopReason(*raw.Choices[0].FinishReason)
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
