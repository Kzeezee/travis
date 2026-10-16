// Package voicein will own audio capture and speech-to-text (DESIGN.md §5.10). Only the
// per-OS support question lives here so far: voice has to degrade to off where no capture
// backend exists, rather than blocking startup (R-19).
package voicein

import "runtime"

// ReportIsCaptureSupported answers whether this OS has a capture backend. An OS without one
// is a normal "voice is off" condition, never a startup failure (R-19, D-10).
func ReportIsCaptureSupported() bool {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		return true
	default:
		return false
	}
}
