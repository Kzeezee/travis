package registry

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

type rankFakeTool struct {
	name        string
	description string
}

func (f rankFakeTool) Name() string { return f.name }
func (f rankFakeTool) Describe() core.ToolSpec {
	return core.ToolSpec{Name: f.name, Description: f.description}
}
func (f rankFakeTool) Invoke(context.Context, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{}, nil
}

func TestRankToolsByHintOverlapPutsTheBestMatchFirst(t *testing.T) {
	tools := []core.Tool{
		rankFakeTool{name: "mail_send", description: "send an email"},
		rankFakeTool{name: "docs_search", description: "search documentation files"},
	}
	ranked := rankToolsByHintOverlap(tools, "search the documentation")
	if ranked[0].Name() != "docs_search" {
		t.Fatalf("ranked[0] = %q, want docs_search", ranked[0].Name())
	}
}

func TestRankToolsByHintOverlapWithEmptyHintPreservesOrder(t *testing.T) {
	tools := []core.Tool{
		rankFakeTool{name: "b"},
		rankFakeTool{name: "a"},
	}
	ranked := rankToolsByHintOverlap(tools, "")
	if ranked[0].Name() != "b" || ranked[1].Name() != "a" {
		t.Fatalf("ranked = %v, want input order preserved", ranked)
	}
}

func TestRankToolsByHintOverlapDoesNotMutateInput(t *testing.T) {
	tools := []core.Tool{
		rankFakeTool{name: "b"},
		rankFakeTool{name: "a", description: "a match"},
	}
	_ = rankToolsByHintOverlap(tools, "match")
	if tools[0].Name() != "b" || tools[1].Name() != "a" {
		t.Fatalf("input slice was mutated: %v", tools)
	}
}

func TestEstimateToolSpecTokensGrowsWithSpecSize(t *testing.T) {
	small := estimateToolSpecTokens(core.ToolSpec{Name: "a"})
	large := estimateToolSpecTokens(core.ToolSpec{Name: "a", Description: "a much longer description of what this tool does"})
	if large <= small {
		t.Fatalf("large estimate %d, want more than small estimate %d", large, small)
	}
}
