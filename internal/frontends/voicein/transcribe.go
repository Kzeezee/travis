package voicein

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/Kzeezee/travis/internal/core"
)

// Transcriber turns one recording, in the PCM format above, into text.
type Transcriber interface {
	Transcribe(ctx context.Context, pcm []byte) (string, error)
}

const (
	// A transcript is a prompt, not a document; anything larger is a misbehaving engine.
	maxTranscriptBytes = 1 << 20
	// A stuck engine leaves the input box empty rather than the status bar saying
	// "transcribing" forever.
	maxTranscriptionTime = 60 * time.Second
)

// BuildWhistleTranscriber transcribes with Cactus Compute's Whistle model, run by its needle
// engine as one process per recording on this machine, so the room's audio never leaves it (D-15).
func BuildWhistleTranscriber(enginePath, modelPath string) Transcriber {
	return engineTranscriber{path: enginePath, modelPath: modelPath}
}

type engineTranscriber struct {
	path string
	// args come before needle's own flags; only tests set them, to run a fake engine.
	args      []string
	modelPath string
}

func (t engineTranscriber) Transcribe(ctx context.Context, pcm []byte) (string, error) {
	recording, err := writeRecordingFile(pcm)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(recording) }()

	ctx, cancel := context.WithTimeout(ctx, maxTranscriptionTime)
	defer cancel()
	// #nosec G204 -- path is the engine installed beside travis; the other args are its model and our own temp file.
	cmd := exec.CommandContext(ctx, t.path, append(slices.Clone(t.args), "--model", t.modelPath, "--audio", recording)...)
	stdout := &cappedBuffer{limit: maxTranscriptBytes}
	stderr := &cappedBuffer{limit: maxHelperErrorBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return "", fmt.Errorf("speech engine: %s: %w", detail, err)
		}
		return "", fmt.Errorf("speech engine: %w", err)
	}

	var result struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Text == nil {
		return "", fmt.Errorf("speech engine sent no transcript: %w", core.ErrProviderMalformed)
	}
	// Whistle returns "" for silence and steady noise, so there are no non-speech markers to strip.
	return strings.Join(strings.Fields(*result.Text), " "), nil
}

// writeRecordingFile puts the recording where the engine can read it. It is room audio, so the
// file is private to this user (CreateTemp's 0600) and the caller removes it once transcribed.
func writeRecordingFile(pcm []byte) (string, error) {
	file, err := os.CreateTemp("", "travis-speech-*.wav")
	if err != nil {
		return "", fmt.Errorf("save the recording: %w", err)
	}
	writeErr := writeWAV(file, pcm)
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("save the recording: %w", writeErr)
	}
	return file.Name(), nil
}

// writeWAV wraps raw PCM in the 44-byte RIFF header the engine reads.
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
