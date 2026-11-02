package voicein

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

const fakeEngineModeEnv = "TRAVIS_VOICEIN_FAKE_ENGINE"

// TestFakeSpeechEngine is not a test: it stands in for needle in the tests below.
func TestFakeSpeechEngine(t *testing.T) {
	mode := os.Getenv(fakeEngineModeEnv)
	if mode == "" {
		t.Skip("only runs as a child process")
	}
	flags := map[string]string{}
	for args := flag.Args(); len(args) >= 2; args = args[2:] {
		flags[args[0]] = args[1]
	}
	switch mode {
	case "transcribe":
		wav, err := os.ReadFile(flags["--audio"])
		if err != nil || !bytes.HasPrefix(wav, []byte("RIFF")) || binary.LittleEndian.Uint32(wav[24:28]) != SampleRate {
			fmt.Fprintln(os.Stderr, "cannot read the audio (RIFF WAV)")
			os.Exit(1)
		}
		fmt.Printf(`{"text":"  heard   %x from %s ","language":"en"}`, wav[44:], filepath.Base(flags["--model"]))
	case "silence":
		fmt.Print(`{"text":"","language":""}`)
	case "garbage":
		fmt.Print("not json")
	case "fail":
		fmt.Fprintln(os.Stderr, "cannot read whistle.cact")
		os.Exit(1)
	}
	os.Exit(0)
}

// transcribeWithFakeEngine also points the temp dir somewhere empty, so the test can see the
// recording file was removed.
func transcribeWithFakeEngine(t *testing.T, mode string, pcm []byte) (text, tempDir string, err error) {
	t.Helper()
	tempDir = t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tempDir)
	}
	t.Setenv(fakeEngineModeEnv, mode)
	engine := engineTranscriber{path: os.Args[0], args: []string{"-test.run=^TestFakeSpeechEngine$", "--"}, modelPath: filepath.Join("models", "whistle.cact")}
	text, err = engine.Transcribe(context.Background(), pcm)
	return text, tempDir, err
}

func TestWhistleTranscriberSendsAWAVAndReturnsTheText(t *testing.T) {
	text, tempDir, err := transcribeWithFakeEngine(t, "transcribe", []byte{1, 0, 2, 0})

	if err != nil || text != "heard 01000200 from whistle.cact" {
		t.Fatalf("text = %q, err = %v", text, err)
	}
	if left, _ := os.ReadDir(tempDir); len(left) != 0 {
		t.Fatalf("recording left behind: %v", left)
	}
}

func TestWhistleTranscriberTurnsSilenceIntoNothing(t *testing.T) {
	text, _, err := transcribeWithFakeEngine(t, "silence", []byte{0, 0})

	if err != nil || text != "" {
		t.Fatalf("text = %q, err = %v; want silence to transcribe as nothing", text, err)
	}
}

func TestWhistleTranscriberReportsTheEnginesError(t *testing.T) {
	_, tempDir, err := transcribeWithFakeEngine(t, "fail", []byte{0, 0})

	if err == nil || !strings.Contains(err.Error(), "cannot read whistle.cact") {
		t.Fatalf("err = %v, want the engine's own words", err)
	}
	if left, _ := os.ReadDir(tempDir); len(left) != 0 {
		t.Fatalf("recording left behind after a failure: %v", left)
	}
}

func TestWhistleTranscriberRejectsOutputWithNoTranscript(t *testing.T) {
	_, _, err := transcribeWithFakeEngine(t, "garbage", []byte{0, 0})

	if !errors.Is(err, core.ErrProviderMalformed) {
		t.Fatalf("err = %v, want ErrProviderMalformed", err)
	}
}

type fixedCapturer struct {
	pcm []byte
	err error
}

func (c fixedCapturer) StartCapture(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(c.pcm)), c.err
}

type recordingTranscriber struct{ received []byte }

func (t *recordingTranscriber) Transcribe(_ context.Context, pcm []byte) (string, error) {
	t.received = pcm
	return "dictated", nil
}

func TestDictationTranscribesWhatWasRecorded(t *testing.T) {
	transcriber := &recordingTranscriber{}
	finish, err := BuildDictation(fixedCapturer{pcm: []byte{9, 9}}, transcriber).StartDictation(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	text, err := finish()

	if err != nil || text != "dictated" || !bytes.Equal(transcriber.received, []byte{9, 9}) {
		t.Fatalf("text = %q, err = %v, sent %v", text, err, transcriber.received)
	}
}

func TestDictationOfNothingSkipsTheEngine(t *testing.T) {
	transcriber := &recordingTranscriber{}
	finish, _ := BuildDictation(fixedCapturer{}, transcriber).StartDictation(context.Background())

	text, err := finish()

	if err != nil || text != "" || transcriber.received != nil {
		t.Fatalf("text = %q, err = %v; want no transcription call", text, err)
	}
}

func TestDictationReportsACaptureThatCannotStart(t *testing.T) {
	wantErr := errors.New("no microphone")

	_, err := BuildDictation(fixedCapturer{err: wantErr}, &recordingTranscriber{}).StartDictation(context.Background())

	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
