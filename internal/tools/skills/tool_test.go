package skills

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Kzeezee/travis/internal/tools/tooltest"
)

func buildTestCatalog() *Catalog {
	return &Catalog{byName: map[string]skill{
		"demo": {Name: "demo", Description: "a demo skill", Body: "demo body text", Tier: TierUser},
	}}
}

func TestOpenToolPassesTheToolContract(t *testing.T) {
	tooltest.Run(t, BuildOpenTool(buildTestCatalog()))
}

func TestOpenToolReturnsTheBodyForAKnownSkill(t *testing.T) {
	tool := BuildOpenTool(buildTestCatalog())
	arguments, err := json.Marshal(map[string]string{"name": "demo"})
	if err != nil {
		t.Fatal(err)
	}
	output, err := tool.Invoke(context.Background(), arguments)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if output.IsError {
		t.Fatalf("Invoke() = %+v, want IsError false", output)
	}
	if output.Content != "demo body text" {
		t.Fatalf("Invoke() content = %q, want the skill's body", output.Content)
	}
	if output.IsUntrusted {
		t.Fatal("user skill was marked as workspace content")
	}
}

func TestOpenToolReportsAnUnknownSkillAsAValueNotAGoError(t *testing.T) {
	tool := BuildOpenTool(buildTestCatalog())
	arguments, err := json.Marshal(map[string]string{"name": "does-not-exist"})
	if err != nil {
		t.Fatal(err)
	}
	output, err := tool.Invoke(context.Background(), arguments)
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil: an unknown skill is a value result, not a Go error", err)
	}
	if !output.IsError {
		t.Fatalf("Invoke() = %+v, want IsError true", output)
	}
}
