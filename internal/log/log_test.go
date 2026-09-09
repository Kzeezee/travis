package log

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerWritesThroughSlog(t *testing.T) {
	var out bytes.Buffer
	logger := BuildTextLogger(&out, LevelDebug)

	logger.LogInfoMsg("provider connected", "provider", "local")

	got := out.String()
	if !strings.Contains(got, "provider connected") {
		t.Fatalf("log output = %q, want message", got)
	}
	if !strings.Contains(got, "provider=local") {
		t.Fatalf("log output = %q, want provider attr", got)
	}
}
