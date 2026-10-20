package voicein

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Kzeezee/travis/internal/core"
)

// Two minutes of speech; a key held down by accident must not record all afternoon.
const maxRecordingBytes = 2 * 60 * SampleRate * ChannelCount * BytesPerSample

// Dictation records while push-to-talk is held and turns the recording into text. It only
// ever produces prose for the input box: nothing it returns can answer an approval (D-10).
type Dictation struct {
	capturer    Capturer
	transcriber Transcriber
}

func BuildDictation(capturer Capturer, transcriber Transcriber) *Dictation {
	return &Dictation{capturer: capturer, transcriber: transcriber}
}

// StartDictation starts recording. finish stops it and returns the transcript; calling finish
// and ignoring the result is how a recording is discarded.
func (d *Dictation) StartDictation(ctx context.Context) (finish func() (string, error), err error) {
	stream, err := d.capturer.StartCapture(ctx)
	if err != nil {
		return nil, err
	}
	recorded := make(chan []byte, 1)
	go func() {
		pcm, _ := io.ReadAll(io.LimitReader(stream, maxRecordingBytes))
		recorded <- pcm
	}()
	return func() (string, error) {
		captureErr := stream.Close()
		pcm := <-recorded
		if captureErr != nil {
			return "", captureErr
		}
		if len(pcm) == 0 {
			return "", nil
		}
		return d.transcriber.Transcribe(ctx, pcm)
	}, nil
}

// ResolveHelperExecutable finds travis-audio next to the running travis, where `make audio`
// and an install put it. Missing is ErrNotFound: voice input is an optional extra.
func ResolveHelperExecutable() (string, error) {
	travisExecutable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the travis binary: %w", err)
	}
	if travisExecutable, err = filepath.EvalSymlinks(travisExecutable); err != nil {
		return "", fmt.Errorf("locate the travis binary: %w", err)
	}
	name := "travis-audio"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	helper := filepath.Join(filepath.Dir(travisExecutable), name)
	if _, err := os.Stat(helper); err != nil {
		return "", fmt.Errorf("voice input needs %s next to travis (build it with `make audio`): %w", name, errors.Join(core.ErrNotFound, err))
	}
	return helper, nil
}
