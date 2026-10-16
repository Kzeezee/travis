package voicein

import (
	"runtime"
	"testing"
)

// G-8: all three OSes are shipped, so the three CI runners must each report themselves
// supported. This also keeps §8.1's forbidigo exemption for this package honest: it is the
// non-test file above that needs it, and lint fails here if the exemption stops matching.
func TestCaptureIsSupportedOnEveryShippedOS(t *testing.T) {
	if !ReportIsCaptureSupported() {
		t.Fatalf("runtime.GOOS = %q reports no capture backend, but it is a shipped OS", runtime.GOOS)
	}
}
