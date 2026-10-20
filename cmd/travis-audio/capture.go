//go:build cgo

package main

import (
	"fmt"
	"io"

	"github.com/gen2brain/malgo"

	"github.com/Kzeezee/travis/internal/frontends/voicein"
)

// ponytail: frames are dropped when travis reads too slowly, never queued without bound; count and report drops if dictation gets gaps.
const bufferedFrameLimit = 64

func captureUntilStdinCloses(stdin io.Reader, stdout io.Writer) error {
	audio, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("open the audio system: %w", err)
	}
	defer func() {
		_ = audio.Uninit()
		audio.Free()
	}()

	config := malgo.DefaultDeviceConfig(malgo.Capture)
	config.Capture.Format = malgo.FormatS16
	config.Capture.Channels = voicein.ChannelCount
	config.SampleRate = voicein.SampleRate

	frames := make(chan []byte, bufferedFrameLimit)
	receiveFrames := func(_, input []byte, _ uint32) {
		select {
		case frames <- append([]byte(nil), input...):
		default:
		}
	}
	device, err := malgo.InitDevice(audio.Context, config, malgo.DeviceCallbacks{Data: receiveFrames})
	if err != nil {
		return fmt.Errorf("open the microphone: %w", err)
	}
	defer device.Uninit()
	if err := device.Start(); err != nil {
		return fmt.Errorf("start the microphone: %w", err)
	}

	stop := make(chan struct{})
	go waitForEOF(stdin, stop)
	for {
		select {
		case <-stop:
			return nil
		case frame := <-frames:
			if _, err := stdout.Write(frame); err != nil {
				return nil // travis stopped reading; that is a stop, not a failure
			}
		}
	}
}

// waitForEOF closes stop once r ends, so a parent that dies also stops the capture.
func waitForEOF(r io.Reader, stop chan<- struct{}) {
	_, _ = io.Copy(io.Discard, r)
	close(stop)
}
