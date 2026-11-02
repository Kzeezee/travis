package voicein

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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

// ModuleFiles are the optional voice-input module, which `make voice` (dev) and
// `make install-voice` put beside travis: the microphone helper, the speech engine, and its model.
type ModuleFiles struct {
	AudioHelper  string
	SpeechEngine string
	SpeechModel  string
}

// ResolveModuleFiles finds the voice-input module beside the running travis. Any part missing is
// ErrNotFound: voice input is an optional extra.
func ResolveModuleFiles() (ModuleFiles, error) {
	travisExecutable, err := os.Executable()
	if err != nil {
		return ModuleFiles{}, fmt.Errorf("locate the travis binary: %w", err)
	}
	if travisExecutable, err = filepath.EvalSymlinks(travisExecutable); err != nil {
		return ModuleFiles{}, fmt.Errorf("locate the travis binary: %w", err)
	}
	return resolveModuleFilesIn(filepath.Dir(travisExecutable))
}

func resolveModuleFilesIn(dir string) (ModuleFiles, error) {
	executableSuffix := ""
	if runtime.GOOS == "windows" {
		executableSuffix = ".exe"
	}
	files := ModuleFiles{
		AudioHelper:  filepath.Join(dir, "travis-audio"+executableSuffix),
		SpeechEngine: filepath.Join(dir, "needle"+executableSuffix),
		SpeechModel:  filepath.Join(dir, "whistle.cact"),
	}
	var missing []string
	for _, path := range []string{files.AudioHelper, files.SpeechEngine, files.SpeechModel} {
		// Stat follows links, so a model link whose target was deleted counts as missing.
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, filepath.Base(path))
		}
	}
	if len(missing) > 0 {
		return ModuleFiles{}, fmt.Errorf("voice input needs %s beside travis (run `make voice`, or `make install-voice` for an installed travis): %w",
			strings.Join(missing, ", "), core.ErrNotFound)
	}
	return files, nil
}
