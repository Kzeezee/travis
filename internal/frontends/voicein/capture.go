package voicein

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// The PCM format travis-audio streams: signed 16-bit little-endian, mono, 16 kHz. It is what
// whisper expects, so a transcriber can send it on without resampling.
const (
	SampleRate     = 16000
	ChannelCount   = 1
	BytesPerSample = 2
)

// Capturer records the microphone. Reading the stream yields PCM in the format above; closing
// it stops the recording.
type Capturer interface {
	StartCapture(ctx context.Context) (io.ReadCloser, error)
}

// maxHelperErrorBytes keeps a helper that floods stderr from growing travis's memory.
const maxHelperErrorBytes = 4 << 10

// BuildHelperCapturer records through the travis-audio helper at helperPath, which keeps cgo
// and the audio libraries out of the travis binary (§5.10, OQ-13).
func BuildHelperCapturer(helperPath string) Capturer {
	return helperCapturer{path: helperPath, args: []string{"capture"}}
}

type helperCapturer struct {
	path string
	args []string
}

func (c helperCapturer) StartCapture(ctx context.Context) (io.ReadCloser, error) {
	// #nosec G204 -- path is travis's own helper binary and args are constants; no input reaches them.
	cmd := exec.CommandContext(ctx, c.path, c.args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &cappedBuffer{limit: maxHelperErrorBytes}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the audio helper: %w", err)
	}
	return &helperStream{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

// helperStream is one recording. The helper stops when its stdin closes; that is the whole
// stop protocol, so a travis that dies never leaves the microphone open.
type helperStream struct {
	cmd    *exec.Cmd
	stdin  io.Closer
	stdout io.Reader
	stderr *cappedBuffer
}

func (s *helperStream) Read(p []byte) (int, error) {
	return s.stdout.Read(p)
}

// Close stops the recording and reports a helper that failed, in the helper's own words.
func (s *helperStream) Close() error {
	_ = s.stdin.Close()
	// Drain so a helper blocked writing a frame can see its stdin close and exit.
	_, _ = io.Copy(io.Discard, s.stdout)
	if err := s.cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("audio helper: %s: %w", strings.TrimSpace(s.stderr.String()), err)
		}
		return fmt.Errorf("audio helper: %w", err)
	}
	return nil
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
