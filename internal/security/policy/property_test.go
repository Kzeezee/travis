package policy

import (
	"context"
	"math/rand/v2"
	"path/filepath"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/capabilities"
)

// Randomised grant sets and requests must never let a deny hit or an ungranted request through.
func TestEvaluateProperties(t *testing.T) {
	r := buildTestRoots(t)
	deny := config.BuildDenyGlobs(config.PathSet{StateDir: r.state}, r.Home)
	denySets := map[bool]capabilities.Set{}
	for _, isCaseInsensitive := range []bool{false, true} {
		denySets[isCaseInsensitive] = New(nil, deny.Read, deny.Write, isCaseInsensitive, "").deny
	}

	grantPool := []capabilities.Set{
		mustBuildProfileSet(t, "paranoid", r.Roots),
		mustBuildProfileSet(t, "default", r.Roots),
		mustBuildProfileSet(t, "dev", r.Roots),
		{{Kind: core.CapabilityFSRead, Scope: "**"}, {Kind: core.CapabilityFSWrite, Scope: "**"}},
		{{Kind: core.CapabilityFSWrite, Scope: filepath.ToSlash(r.Home) + "/**"}},
	}
	rootPool := []string{r.Workspace, r.Home, r.Temp, r.state}
	segmentPool := []string{".ssh", ".SSH", "id_rsa", ".env", ".ENV", ".env.local", ".travis", ".git", "hooks", "src", "Main.go", ".bashrc", "traces"}
	kindPool := []core.CapabilityKind{core.CapabilityFSRead, core.CapabilityFSWrite}

	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 10_000 {
		var grants capabilities.Set
		for _, set := range grantPool {
			if rng.IntN(2) == 0 {
				grants = append(grants, set...)
			}
		}
		target := rootPool[rng.IntN(len(rootPool))]
		for range 1 + rng.IntN(4) {
			target = filepath.Join(target, segmentPool[rng.IntN(len(segmentPool))])
		}
		isCaseInsensitive := rng.IntN(2) == 0
		req := Request{Kind: kindPool[rng.IntN(len(kindPool))], Target: target, IsFromUntrustedStep: rng.IntN(4) == 0}

		got := New(grants, deny.Read, deny.Write, isCaseInsensitive, "").Evaluate(context.Background(), req)
		if _, isDenyHit := denySets[isCaseInsensitive].SelectMatching(req.Kind, target, isCaseInsensitive); isDenyHit && got.Verdict != Deny {
			t.Fatalf("iteration %d: deny hit %+v (caseInsensitive=%v) got %s", i, req, isCaseInsensitive, got.Verdict)
		}
		if _, isGranted := grants.SelectMatching(req.Kind, target, isCaseInsensitive); !isGranted && got.Verdict == Allow {
			t.Fatalf("iteration %d: ungranted %+v got allow", i, req)
		}
	}
}

func mustBuildProfileSet(t *testing.T, profile string, r capabilities.Roots) capabilities.Set {
	t.Helper()
	set, err := capabilities.BuildProfileSet(profile, r)
	if err != nil {
		t.Fatal(err)
	}
	return set
}
