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

func TestRankToolsByQueryOverlapPutsTheBestMatchFirst(t *testing.T) {
	tools := []core.Tool{
		rankFakeTool{name: "mail_send", description: "send an email"},
		rankFakeTool{name: "docs_search", description: "search documentation files"},
	}
	ranked := rankToolsByQueryOverlap(tools, "search the documentation")
	if ranked[0].Name() != "docs_search" {
		t.Fatalf("ranked[0] = %q, want docs_search", ranked[0].Name())
	}
}

func TestRankToolsByQueryOverlapDropsToolsWithNoOverlap(t *testing.T) {
	tools := []core.Tool{
		rankFakeTool{name: "b"},
		rankFakeTool{name: "a", description: "a match"},
	}
	if ranked := rankToolsByQueryOverlap(tools, "match"); len(ranked) != 1 || ranked[0].Name() != "a" {
		t.Fatalf("ranked = %v, want only a", ranked)
	}
	if ranked := rankToolsByQueryOverlap(tools, ""); len(ranked) != 0 {
		t.Fatalf("empty query ranked %v, want none", ranked)
	}
}

func TestRankToolsByQueryOverlapKeepsInputOrderForTies(t *testing.T) {
	tools := []core.Tool{
		rankFakeTool{name: "b", description: "match"},
		rankFakeTool{name: "a", description: "match"},
	}
	ranked := rankToolsByQueryOverlap(tools, "match")
	if ranked[0].Name() != "b" || ranked[1].Name() != "a" {
		t.Fatalf("ranked = %v, want input order preserved", ranked)
	}
}

func TestEstimateToolSpecTokensGrowsWithSpecSize(t *testing.T) {
	small := estimateToolSpecTokens(core.ToolSpec{Name: "a"})
	large := estimateToolSpecTokens(core.ToolSpec{Name: "a", Description: "a much longer description of what this tool does"})
	if large <= small {
		t.Fatalf("large estimate %d, want more than small estimate %d", large, small)
	}
}
