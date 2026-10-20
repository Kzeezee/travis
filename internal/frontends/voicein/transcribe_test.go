package voicein

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type receivedForm struct {
	path   string
	model  string
	wav    []byte
	format string
}

func startTranscriptionServer(t *testing.T, status int, reply string) (string, *receivedForm) {
	t.Helper()
	received := &receivedForm{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.path = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			received.model = r.FormValue("model")
			received.format = r.FormValue("response_format")
			if file, _, err := r.FormFile("file"); err == nil {
				received.wav, _ = io.ReadAll(file)
			}
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/v1", received
}

func TestHTTPTranscriberSendsAWAVAndReturnsTheText(t *testing.T) {
	baseURL, received := startTranscriptionServer(t, http.StatusOK, `{"text":"  hello   travis "}`)
	pcm := []byte{1, 0, 2, 0}

	text, err := BuildHTTPTranscriber(baseURL, "whisper-1", nil).Transcribe(context.Background(), pcm)

	if err != nil || text != "hello travis" {
		t.Fatalf("text = %q, err = %v", text, err)
	}
	if received.path != "/v1/audio/transcriptions" || received.model != "whisper-1" || received.format != "json" {
		t.Fatalf("request = %+v", received)
	}
	if !bytes.HasPrefix(received.wav, []byte("RIFF")) || !bytes.HasSuffix(received.wav, pcm) {
		t.Fatalf("file is not a WAV of the recording: % x", received.wav)
	}
	if rate := binary.LittleEndian.Uint32(received.wav[24:28]); rate != SampleRate {
		t.Fatalf("WAV sample rate = %d, want %d", rate, SampleRate)
	}
}

func TestHTTPTranscriberDropsNonSpeechMarkers(t *testing.T) {
	baseURL, _ := startTranscriptionServer(t, http.StatusOK, `{"text":" [BLANK_AUDIO] (music) "}`)

	text, err := BuildHTTPTranscriber(baseURL, "whisper-1", nil).Transcribe(context.Background(), []byte{0, 0})

	if err != nil || text != "" {
		t.Fatalf("text = %q, err = %v; want silence to transcribe as nothing", text, err)
	}
}

func TestHTTPTranscriberReportsTheServersError(t *testing.T) {
	baseURL, _ := startTranscriptionServer(t, http.StatusBadRequest, "model not loaded")

	_, err := BuildHTTPTranscriber(baseURL, "whisper-1", nil).Transcribe(context.Background(), []byte{0, 0})

	if err == nil || !strings.Contains(err.Error(), "model not loaded") {
		t.Fatalf("err = %v, want the server's own words", err)
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

func TestDictationOfNothingSkipsTheServer(t *testing.T) {
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
