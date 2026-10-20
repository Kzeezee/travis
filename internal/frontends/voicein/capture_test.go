package voicein

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

const fakeHelperModeEnv = "TRAVIS_VOICEIN_FAKE_HELPER"

// TestFakeAudioHelper is not a test: it stands in for travis-audio in the tests below.
func TestFakeAudioHelper(t *testing.T) {
	switch os.Getenv(fakeHelperModeEnv) {
	case "":
		t.Skip("only runs as a child process")
	case "pcm":
		_, _ = os.Stdout.WriteString("pcm-bytes")
	case "flood":
		go func() {
			chunk := make([]byte, 64<<10)
			for {
				if _, err := os.Stdout.Write(chunk); err != nil {
					os.Exit(0)
				}
			}
		}()
	case "fail":
		fmt.Fprintln(os.Stderr, "no capture device")
		os.Exit(1)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func startFakeHelper(t *testing.T, mode string) io.ReadCloser {
	t.Helper()
	t.Setenv(fakeHelperModeEnv, mode)
	capturer := helperCapturer{path: os.Args[0], args: []string{"-test.run=^TestFakeAudioHelper$"}}
	stream, err := capturer.StartCapture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func TestHelperStreamDeliversPCMAndStopsWhenClosed(t *testing.T) {
	stream := startFakeHelper(t, "pcm")

	got := make([]byte, len("pcm-bytes"))
	if _, err := io.ReadFull(stream, got); err != nil || string(got) != "pcm-bytes" {
		t.Fatalf("read %q, err %v", got, err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() = %v, want a clean stop", err)
	}
}

// A helper blocked writing a frame nobody reads must still stop, or the microphone stays open.
func TestHelperStreamStopsAHelperThatIsStillWriting(t *testing.T) {
	stream := startFakeHelper(t, "flood")

	closed := make(chan error, 1)
	go func() { closed <- stream.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close() = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close() hung on a helper that was still writing")
	}
}

func TestHelperStreamReportsTheHelpersOwnError(t *testing.T) {
	stream := startFakeHelper(t, "fail")
	_, _ = io.Copy(io.Discard, stream)

	err := stream.Close()

	if err == nil || !strings.Contains(err.Error(), "no capture device") {
		t.Fatalf("Close() = %v, want the helper's stderr", err)
	}
}

func TestCappedBufferKeepsOnlyTheLimit(t *testing.T) {
	buffer := cappedBuffer{limit: 4}

	n, _ := buffer.Write([]byte("abcdef"))

	if n != 6 || buffer.String() != "abcd" {
		t.Fatalf("wrote %d, kept %q", n, buffer.String())
	}
}
