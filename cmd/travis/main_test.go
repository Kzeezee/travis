package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestVersionStringReportsPlatform(t *testing.T) {
	got := versionString()

	if !strings.HasPrefix(got, "travis ") {
		t.Errorf("version string should start with %q, got %q", "travis ", got)
	}
	// The CI matrix relies on this to identify which runner produced the output.
	if want := runtime.GOOS + "/" + runtime.GOARCH; !strings.Contains(got, want) {
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
			err := run(tt.args, &out)

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
