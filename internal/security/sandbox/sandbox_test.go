package sandbox_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/sandbox"
)

func buildValidSpec(t *testing.T) sandbox.Spec {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	return sandbox.Spec{Launcher: self, Argv: []string{self}, Dir: scratch, WritePaths: []string{scratch}}
}

func encodeSpec(t *testing.T, spec sandbox.Spec) string {
	t.Helper()
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// Both entrypoints validate before reaching the OS backend, so a bad spec is ErrInvalid, never ErrUnsupported.
func TestSandboxRejectsInvalidSpecs(t *testing.T) {
	cases := map[string]func(*sandbox.Spec){
		"empty argv":          func(spec *sandbox.Spec) { spec.Argv = nil },
		"relative launcher":   func(spec *sandbox.Spec) { spec.Launcher = "travis-broker" },
		"relative dir":        func(spec *sandbox.Spec) { spec.Dir = "scratch" },
		"relative read path":  func(spec *sandbox.Spec) { spec.ReadPaths = []string{"docs"} },
		"relative write path": func(spec *sandbox.Spec) { spec.WritePaths = []string{"out"} },
	}
	for name, breakSpec := range cases {
		t.Run(name, func(t *testing.T) {
			spec := buildValidSpec(t)
			breakSpec(&spec)
			if _, _, err := sandbox.BuildCommand(t.Context(), spec); !errors.Is(err, core.ErrInvalid) {
				t.Errorf("BuildCommand error = %v, want ErrInvalid", err)
			}
			if err := sandbox.RunLauncher([]string{encodeSpec(t, spec)}); !errors.Is(err, core.ErrInvalid) {
				t.Errorf("RunLauncher error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestLauncherRejectsMalformedArguments(t *testing.T) {
	valid := encodeSpec(t, buildValidSpec(t))
	cases := map[string][]string{
		"no spec":        nil,
		"two specs":      {valid, valid},
		"malformed json": {"{not json"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if err := sandbox.RunLauncher(args); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("RunLauncher error = %v, want ErrInvalid", err)
			}
		})
	}
}

// The security screen's sandbox line must name the tier a launch would actually get.
func TestReportAvailabilityMatchesWhatBuildCommandConfinesWith(t *testing.T) {
	tier, err := sandbox.ReportAvailability()
	_, builtTier, buildErr := sandbox.BuildCommand(t.Context(), buildValidSpec(t))
	if tier != builtTier || errors.Is(err, core.ErrUnsupported) != errors.Is(buildErr, core.ErrUnsupported) {
		t.Fatalf("ReportAvailability = %q, %v; BuildCommand = %q, %v; want the same tier", tier, err, builtTier, buildErr)
	}
}
