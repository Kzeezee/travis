package policy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/capabilities"
)

type testRoots struct {
	capabilities.Roots
	state string
}

func buildTestRoots(t *testing.T) testRoots {
	base := t.TempDir()
	return testRoots{
		Roots: capabilities.Roots{
			Workspace: filepath.Join(base, "ws"),
			Temp:      filepath.Join(base, "tmp"),
			Home:      filepath.Join(base, "home"),
		},
		state: filepath.Join(base, "state"),
	}
}

func buildTestPolicy(t *testing.T, r testRoots, profile string, isCaseInsensitive bool) Policy {
	t.Helper()
	grants, err := capabilities.BuildProfileSet(profile, r.Roots)
	if err != nil {
		t.Fatal(err)
	}
	deny := config.BuildDenyGlobs(config.PathSet{StateDir: r.state}, r.Home)
	return New(grants, deny.Read, deny.Write, isCaseInsensitive, "")
}

func TestEvaluate(t *testing.T) {
	r := buildTestRoots(t)
	ws, home := r.Workspace, r.Home

	tests := []struct {
		name              string
		profile           string
		isCaseInsensitive bool
		req               Request
		want              Verdict
	}{
		{"granted workspace read", "default", false, Request{Kind: core.CapabilityFSRead, Target: filepath.Join(ws, "main.go")}, Allow},
		{"deny beats dev home grant", "dev", false, Request{Kind: core.CapabilityFSRead, Target: filepath.Join(home, ".ssh", "id_rsa")}, Deny},
		{"case variant denied when insensitive", "dev", true, Request{Kind: core.CapabilityFSRead, Target: filepath.Join(home, ".SSH", "id_rsa")}, Deny},
		{"env file in workspace denied", "default", false, Request{Kind: core.CapabilityFSRead, Target: filepath.Join(ws, "sub", ".env.local")}, Deny},
		{"decisions.toml write-denied", "default", false, Request{Kind: core.CapabilityFSWrite, Target: filepath.Join(ws, ".travis", "decisions.toml")}, Deny},
		{"decisions.toml still readable", "default", false, Request{Kind: core.CapabilityFSRead, Target: filepath.Join(ws, ".travis", "decisions.toml")}, Allow},
		{"git hook write-denied", "default", false, Request{Kind: core.CapabilityFSWrite, Target: filepath.Join(ws, ".git", "hooks", "pre-commit")}, Deny},
		{"state dir read-denied", "dev", false, Request{Kind: core.CapabilityFSRead, Target: filepath.Join(r.state, "traces", "run.jsonl")}, Deny},
		{"write outside grant", "default", false, Request{Kind: core.CapabilityFSWrite, Target: filepath.Join(home, "notes.txt")}, Deny},
		{"paranoid cannot write", "paranoid", false, Request{Kind: core.CapabilityFSWrite, Target: filepath.Join(ws, "main.go")}, Deny},
		{"dev exec asks", "dev", false, Request{Kind: core.CapabilityExec, Target: "go"}, Ask},
		{"default exec denied without allowlist", "default", false, Request{Kind: core.CapabilityExec, Target: "go"}, Deny},
		{"default net denied without allowlist", "default", false, Request{Kind: core.CapabilityNetworkHTTP, Target: "example.com"}, Deny},
		{"dev net allowed", "dev", false, Request{Kind: core.CapabilityNetworkHTTP, Target: "example.com"}, Allow},
		{"untrusted step asks", "default", false, Request{Kind: core.CapabilityFSRead, Target: filepath.Join(ws, "main.go"), IsFromUntrustedStep: true}, Ask},
		{"dotdot unresolved", "dev", false, Request{Kind: core.CapabilityFSRead, Target: ws + string(filepath.Separator) + ".." + string(filepath.Separator) + "x"}, Deny},
		{"relative unresolved", "dev", false, Request{Kind: core.CapabilityFSRead, Target: "main.go"}, Deny},
		{"device namespace unresolved", "dev", false, Request{Kind: core.CapabilityFSRead, Target: `\\?\C:\Users\x\f.txt`}, Deny},
		{"audio has no grant", "dev", false, Request{Kind: core.CapabilityAudioCapture, Target: "default"}, Deny},
		{"unknown kind has no grant", "dev", false, Request{Kind: "fs.read", Target: filepath.Join(ws, "main.go")}, Deny},
	}
	for _, tt := range tests {
		p := buildTestPolicy(t, r, tt.profile, tt.isCaseInsensitive)
		got := p.Evaluate(context.Background(), tt.req)
		if got.Verdict != tt.want {
			t.Errorf("%s: Evaluate(%+v) = %s, want %s\n%s", tt.name, tt.req, got.Verdict, tt.want, strings.Join(got.Trace, "\n"))
		}
		if len(got.Trace) == 0 || !strings.HasPrefix(got.Trace[len(got.Trace)-1], got.Verdict.String()+":") {
			t.Errorf("%s: trace %q does not end with the verdict", tt.name, got.Trace)
		}
	}
}

func TestExplainJoinsTrace(t *testing.T) {
	r := buildTestRoots(t)
	p := buildTestPolicy(t, r, "dev", false)
	explanation := p.Explain(Request{Kind: core.CapabilityFSRead, Target: filepath.Join(r.Home, ".ssh", "id_rsa")})
	if !strings.Contains(explanation, "deny:") || !strings.Contains(explanation, ".ssh/**") {
		t.Fatalf("Explain() = %q, want the matching deny rule", explanation)
	}
}
