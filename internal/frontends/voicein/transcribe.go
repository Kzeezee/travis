package voicein

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

// Transcriber turns one recording, in the PCM format above, into text.
type Transcriber interface {
	Transcribe(ctx context.Context, pcm []byte) (string, error)
}

const (
	// A transcript is a prompt, not a document; anything larger is a misbehaving server.
	maxTranscriptResponseBytes = 1 << 20
	maxErrorDetailBytes        = 2 << 10
)

// whisper.cpp marks non-speech as "[BLANK_AUDIO]", "(music)" and the like; none of it is dictation.
var nonSpeechMarker = regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)`)

// BuildHTTPTranscriber speaks to an OpenAI-style POST {baseURL}/audio/transcriptions, which
// whisper.cpp's server offers locally (§5.10: whisper_server is the default backend).
func BuildHTTPTranscriber(baseURL, model string, client *http.Client) Transcriber {
	if client == nil {
		client = http.DefaultClient
	}
	return httpTranscriber{baseURL: strings.TrimSuffix(baseURL, "/"), model: model, client: client}
}

type httpTranscriber struct {
	baseURL string
	model   string
	client  *http.Client
}

func (t httpTranscriber) Transcribe(ctx context.Context, pcm []byte) (string, error) {
	endpoint, err := url.JoinPath(t.baseURL, "audio", "transcriptions")
	if err != nil {
		return "", fmt.Errorf("speech-to-text address %q: %w", t.baseURL, core.ErrInvalid)
	}
	body, contentType, err := buildTranscriptionForm(pcm, t.model)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return "", fmt.Errorf("speech-to-text request: %w", err)
	}
	request.Header.Set("Content-Type", contentType)

	response, err := t.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("speech-to-text server at %s is not reachable: %w", t.baseURL, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorDetailBytes))
		return "", fmt.Errorf("speech-to-text server: %s: %s", response.Status, strings.TrimSpace(string(detail)))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxTranscriptResponseBytes)).Decode(&result); err != nil {
		return "", fmt.Errorf("speech-to-text server sent no transcript: %w", core.ErrProviderMalformed)
	}
	return strings.Join(strings.Fields(nonSpeechMarker.ReplaceAllString(result.Text, " ")), " "), nil
}

func buildTranscriptionForm(pcm []byte, model string) (io.Reader, string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "speech.wav")
	if err != nil {
		return nil, "", err
	}
	if err := writeWAV(file, pcm); err != nil {
		return nil, "", err
	}
	for name, value := range map[string]string{"model": model, "response_format": "json"} {
		if err := form.WriteField(name, value); err != nil {
			return nil, "", err
		}
	}
	if err := form.Close(); err != nil {
		return nil, "", err
	}
	return &body, form.FormDataContentType(), nil
}

// writeWAV wraps raw PCM in the 44-byte RIFF header every transcription server accepts.
func writeWAV(w io.Writer, pcm []byte) error {
	const headerSize, formatChunkSize, pcmFormat, bitsPerSample = 36, 16, 1, BytesPerSample * 8
	if uint64(len(pcm)) > math.MaxUint32-headerSize {
		return fmt.Errorf("recording of %d bytes is too long for a WAV file: %w", len(pcm), core.ErrInvalid)
	}
	dataSize := uint32(len(pcm)) //nolint:gosec // bounded by the check above
	header := []any{
		[4]byte{'R', 'I', 'F', 'F'}, headerSize + dataSize, [4]byte{'W', 'A', 'V', 'E'},
		[4]byte{'f', 'm', 't', ' '}, uint32(formatChunkSize), uint16(pcmFormat), uint16(ChannelCount),
		uint32(SampleRate), uint32(SampleRate * ChannelCount * BytesPerSample), uint16(ChannelCount * BytesPerSample), uint16(bitsPerSample),
		[4]byte{'d', 'a', 't', 'a'}, dataSize,
	}
	for _, field := range header {
		if err := binary.Write(w, binary.LittleEndian, field); err != nil {
			return err
		}
	}
	_, err := w.Write(pcm)
	return err
}
