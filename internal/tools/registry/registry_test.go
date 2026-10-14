package registry_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/tools/registry"
)

type fakeTool struct {
	name        string
	specName    string
	description string
}

func buildFakeTool(name string) fakeTool {
	return fakeTool{name: name, specName: name}
}

func buildFakeToolWithDescription(name, description string) fakeTool {
	return fakeTool{name: name, specName: name, description: description}
}

func (f fakeTool) Name() string { return f.name }

func (f fakeTool) Describe() core.ToolSpec {
	return core.ToolSpec{Name: f.specName, Description: f.description}
}

func (f fakeTool) Invoke(context.Context, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{}, nil
}

func reportNames(tools []core.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name()
	}
	return names
}

func assertNames(t *testing.T, got []core.Tool, want ...string) {
	t.Helper()
	gotNames := reportNames(got)
	if strings.Join(gotNames, ",") != strings.Join(want, ",") {
		t.Fatalf("names = %v, want %v", gotNames, want)
	}
}

func mustRegister(t *testing.T, r *registry.Registry, source registry.Source, name string) {
	t.Helper()
	if err := r.Register(source, buildFakeTool(name)); err != nil {
		t.Fatalf("Register(%q): %v", name, err)
	}
}

func TestRegisterRejectsNamesProvidersWouldRefuse(t *testing.T) {
	for _, name := range []string{"", "fs.read", "mcp.github.create_issue", "has space", "ünïcode", strings.Repeat("a", 65)} {
		t.Run(name, func(t *testing.T) {
			r := registry.BuildRegistry(10)
			if err := r.Register(registry.SourceBuiltin, buildFakeTool(name)); err == nil {
				t.Fatalf("expected Register(%q) to fail", name)
			}
		})
	}
}

func TestRegisterAcceptsNamespacedNames(t *testing.T) {
	r := registry.BuildRegistry(10)
	for _, name := range []string{"fs_read", "grep", "ask_user", "mcp__github__create_issue", "kebab-case", strings.Repeat("a", 64)} {
		mustRegister(t, r, registry.SourceBuiltin, name)
	}
}

func TestRegisterRejectsANameThatDisagreesWithDescribe(t *testing.T) {
	r := registry.BuildRegistry(10)
	tool := fakeTool{name: "fs_read", specName: "fs_reed"}
	if err := r.Register(registry.SourceBuiltin, tool); err == nil {
		t.Fatal("expected an error for a Name/Describe mismatch")
	}
}

func TestRegisterCollisionNamesBothSources(t *testing.T) {
	r := registry.BuildRegistry(10)
	mustRegister(t, r, registry.SourceBuiltin, "fs_read")

	err := r.Register(registry.SourceMCP, buildFakeTool("fs_read"))
	if err == nil {
		t.Fatal("expected a collision error")
	}
	for _, want := range []string{"fs_read", string(registry.SourceBuiltin), string(registry.SourceMCP)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	tool, _ := r.Lookup("fs_read")
	if tool.Describe().Name != "fs_read" {
		t.Fatal("original registration was overwritten")
	}
}

func TestNamespacedNamesFromDifferentSourcesCoexist(t *testing.T) {
	r := registry.BuildRegistry(10)
	mustRegister(t, r, registry.SourceBuiltin, "fs_read")
	mustRegister(t, r, registry.SourceMCP, "mcp__fs__read")

	for _, name := range []string{"fs_read", "mcp__fs__read"} {
		if _, isFound := r.Lookup(name); !isFound {
			t.Errorf("Lookup(%q) not found", name)
		}
	}
}

func TestLookupReportsMissingTools(t *testing.T) {
	r := registry.BuildRegistry(10)
	if _, isFound := r.Lookup("fs_read"); isFound {
		t.Fatal("expected a miss on an empty registry")
	}
}

func TestListFiltersBySourceAndPrefix(t *testing.T) {
	r := registry.BuildRegistry(10)
	mustRegister(t, r, registry.SourceBuiltin, "fs_write")
	mustRegister(t, r, registry.SourceBuiltin, "fs_read")
	mustRegister(t, r, registry.SourceBuiltin, "exec")
	mustRegister(t, r, registry.SourceMCP, "mcp__github__issues")

	assertNames(t, r.List(registry.Filter{}), "exec", "fs_read", "fs_write", "mcp__github__issues")
	assertNames(t, r.List(registry.Filter{Source: registry.SourceMCP}), "mcp__github__issues")
	assertNames(t, r.List(registry.Filter{NamePrefix: "fs_"}), "fs_read", "fs_write")
	assertNames(t, r.List(registry.Filter{Source: registry.SourceMCP, NamePrefix: "fs_"}))
}

func TestUnregisterRemovesExactlyThoseTools(t *testing.T) {
	r := registry.BuildRegistry(10)
	mustRegister(t, r, registry.SourceMCP, "mcp_a_one")
	mustRegister(t, r, registry.SourceMCP, "mcp_a_two")
	mustRegister(t, r, registry.SourceMCP, "mcp_b_one")

	r.Unregister("mcp_a_one", "mcp_a_two")

	assertNames(t, r.List(registry.Filter{}), "mcp_b_one")
	if _, isFound := r.Lookup("mcp_a_one"); isFound {
		t.Fatal("mcp_a_one should have been removed")
	}
}

func TestUnregisterOfAMissingNameIsANoOp(t *testing.T) {
	r := registry.BuildRegistry(10)
	mustRegister(t, r, registry.SourceBuiltin, "fs_read")
	r.Unregister("never_registered")
	assertNames(t, r.List(registry.Filter{}), "fs_read")
}

func TestPruneAlwaysExposesBuiltinSkillAndSwarmTools(t *testing.T) {
	r := registry.BuildRegistry(2)
	mustRegister(t, r, registry.SourceBuiltin, "fs_read")
	mustRegister(t, r, registry.SourceBuiltin, "fs_write")
	mustRegister(t, r, registry.SourceBuiltin, "grep")
	mustRegister(t, r, registry.SourceSkill, "skill_open")
	mustRegister(t, r, registry.SourceSwarm, "spawn_agent")

	// maxExposed=2 is smaller than the 5 always-exposed tools above; none are dropped.
	assertNames(t, r.Prune(context.Background(), 0, ""), "fs_read", "fs_write", "grep", "skill_open", "spawn_agent")
}

func TestPruneRanksMCPToolsByHintOverlapAndCapsAtMaxExposed(t *testing.T) {
	r := registry.BuildRegistry(2)
	mustRegister(t, r, registry.SourceBuiltin, "ask_user")
	if err := r.Register(registry.SourceMCP, buildFakeToolWithDescription("mcp_docs_search", "search documentation")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(registry.SourceMCP, buildFakeToolWithDescription("mcp_mail_send", "send an email")); err != nil {
		t.Fatal(err)
	}

	// maxExposed=2: ask_user (always exposed) leaves exactly one MCP slot; the hint should
	// pick the docs tool over the mail tool for it.
	assertNames(t, r.Prune(context.Background(), 0, "search the documentation"), "ask_user", "mcp_docs_search")
}

func TestPruneWithNoHintKeepsMCPToolsInListOrder(t *testing.T) {
	r := registry.BuildRegistry(10)
	for _, name := range []string{"mcp_x_c", "mcp_x_a", "mcp_x_b"} {
		mustRegister(t, r, registry.SourceMCP, name)
	}
	assertNames(t, r.Prune(context.Background(), 0, ""), "mcp_x_a", "mcp_x_b", "mcp_x_c")
}

func TestPruneIsStableAcrossCalls(t *testing.T) {
	r := registry.BuildRegistry(10)
	for _, name := range []string{"c", "a", "b"} {
		mustRegister(t, r, registry.SourceBuiltin, name)
	}
	first := reportNames(r.Prune(context.Background(), 0, ""))
	for range 20 {
		assertNames(t, r.Prune(context.Background(), 0, ""), first...)
	}
}

// TestPruneKeepsMCPToolSchemaTokensUnderBudget is the ticket's required proof: with three
// fake servers of 20 tools each (60 MCP tools total), a tight token budget must still leave
// the returned schemas' estimated cost at or under that budget.
func TestPruneKeepsMCPToolSchemaTokensUnderBudget(t *testing.T) {
	r := registry.BuildRegistry(1000)
	mustRegister(t, r, registry.SourceBuiltin, "ask_user")

	longDescription := strings.Repeat("this tool does something useful with your files. ", 4)
	for server := range 3 {
		for tool := range 20 {
			name := fmt.Sprintf("mcp_server%d_tool%d", server, tool)
			if err := r.Register(registry.SourceMCP, buildFakeToolWithDescription(name, longDescription)); err != nil {
				t.Fatal(err)
			}
		}
	}

	const budget = 500 // far less than all 60 MCP tools' schemas would cost
	pruned := r.Prune(context.Background(), budget, "")

	var estimatedTotal int
	for _, tool := range pruned {
		spec := tool.Describe()
		estimatedTotal += (len(spec.Name) + len(spec.Description) + len(spec.InputSchema)) / 4
	}
	if estimatedTotal > budget {
		t.Fatalf("pruned set costs an estimated %d tokens, want at most %d", estimatedTotal, budget)
	}
	if len(pruned) >= 61 {
		t.Fatalf("pruned kept all 60 MCP tools plus ask_user; budget should have cut some")
	}
	if pruned[0].Name() != "ask_user" {
		t.Fatalf("pruned[0] = %q, want the always-exposed ask_user first", pruned[0].Name())
	}
}

func TestConcurrentLookupsAreSafe(t *testing.T) {
	r := registry.BuildRegistry(10)
	mustRegister(t, r, registry.SourceBuiltin, "fs_read")

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if _, isFound := r.Lookup("fs_read"); !isFound {
					t.Error("Lookup lost a registration")
					return
				}
				_ = r.List(registry.Filter{})
			}
		}()
	}
	wg.Wait()
}
