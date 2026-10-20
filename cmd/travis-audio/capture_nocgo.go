//go:build !cgo

package main

import (
	"errors"
	"io"
)

// Without cgo there is no miniaudio, so this build can only explain itself.
func captureUntilStdinCloses(io.Reader, io.Writer) error {
	return errors.New("this build has no audio support; rebuild with CGO_ENABLED=1 and a C compiler")
}
