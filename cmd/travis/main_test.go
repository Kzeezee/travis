package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
)

func TestVersionStringReportsPlatform(t *testing.T) {
	got := buildVersionString()

	if !strings.HasPrefix(got, "travis ") {
		t.Errorf("version string should start with %q, got %q", "travis ", got)
	}
	if want := config.ReportRuntimePlatform(); !strings.Contains(got, want) {
		t.Errorf("version string should contain %q, got %q", want, got)
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		wantOut string
	}{
		{name: "version", args: []string{"version"}, wantOut: "travis "},
		{name: "version long flag", args: []string{"--version"}, wantOut: "travis "},
		{name: "version short flag", args: []string{"-v"}, wantOut: "travis "},
		{name: "no args", args: nil, wantErr: true},
		{name: "unknown command", args: []string{"frobnicate"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(tt.args, strings.NewReader(""), &out)

			if (err != nil) != tt.wantErr {
				t.Fatalf("run(%q) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if tt.wantOut != "" && !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("run(%q) output = %q, want it to contain %q", tt.args, out.String(), tt.wantOut)
			}
			if tt.wantErr && out.Len() != 0 {
				t.Errorf("run(%q) wrote %q to stdout on error; errors belong on stderr", tt.args, out.String())
			}
		})
	}
}

// The chat command wires a real provider, so this only proves the composition holds
// together: the frontend refuses non-terminal I/O, so nothing reaches the network.
func TestRunChatWiresTheSliceWithoutCallingTheProvider(t *testing.T) {
	travisHome := t.TempDir()
	t.Setenv("TRAVIS_HOME", travisHome)
	var out bytes.Buffer

	err := run([]string{"chat", "--model", "test-model"}, strings.NewReader(""), &out)
	if !errors.Is(err, terminal.ErrNotInteractive) {
		t.Fatalf("run(chat) error = %v, want %v", err, terminal.ErrNotInteractive)
	}

	traces, err := filepath.Glob(filepath.Join(travisHome, "state", "traces", "*.jsonl"))
	if err != nil || len(traces) != 1 {
		t.Fatalf("got traces %v (%v), want one session trace", traces, err)
	}
}
