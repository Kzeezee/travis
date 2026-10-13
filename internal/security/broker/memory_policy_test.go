package broker_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/security/policy"
	"github.com/Kzeezee/travis/internal/state/memory"
)

func TestOnlyTheManagedMemoryOperationCanReachThePrivateMemoryFile(t *testing.T) {
	root := t.TempDir()
	paths := config.PathSet{WorkspaceDir: filepath.Join(root, "workspace"), StateDir: filepath.Join(root, "state")}
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	resolvedState, err := filepath.EvalSymlinks(paths.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(resolvedState, memory.FileName)
	for _, profile := range []string{"default", "paranoid"} {
		t.Run(profile, func(t *testing.T) {
			p, err := broker.BuildSessionPolicy(profile, broker.SessionRoots{Paths: paths, Home: root, Temp: filepath.Join(root, "temp")})
			if err != nil {
				t.Fatal(err)
			}
			for _, kind := range []core.CapabilityKind{core.CapabilityFSRead, core.CapabilityFSWrite} {
				request := policy.Request{Kind: kind, Target: target}
				if got := p.Evaluate(context.Background(), request).Verdict; got != policy.Deny {
					t.Fatalf("ordinary %s of memory = %v, want Deny", kind, got)
				}
				request.IsManagedMemory = true
				want := policy.Allow
				if profile == "paranoid" && kind == core.CapabilityFSWrite {
					want = policy.Deny
				}
				if got := p.Evaluate(context.Background(), request).Verdict; got != want {
					t.Fatalf("managed %s of memory = %v, want %v", kind, got, want)
				}
				if want == policy.Allow {
					request.IsFromUntrustedStep = true
					if got := p.Evaluate(context.Background(), request).Verdict; got != policy.Ask {
						t.Fatalf("untrusted managed %s = %v, want Ask", kind, got)
					}
				}
			}
		})
	}
}
