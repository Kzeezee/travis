package registry_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/tools/registry"
)

type fakeTool struct {
	name     string
	specName string
}

func buildFakeTool(name string) fakeTool {
	return fakeTool{name: name, specName: name}
}

func (f fakeTool) Name() string { return f.name }

func (f fakeTool) Describe() core.ToolSpec { return core.ToolSpec{Name: f.specName} }

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

func TestRegisterRejectsAnEmptyName(t *testing.T) {
	r := registry.BuildRegistry(10)
	if err := r.Register(registry.SourceBuiltin, buildFakeTool("")); err == nil {
		t.Fatal("expected an error for an empty name")
	}
}

func TestRegisterRejectsANameThatDisagreesWithDescribe(t *testing.T) {
	r := registry.BuildRegistry(10)
	tool := fakeTool{name: "fs.read", specName: "fs.reed"}
	if err := r.Register(registry.SourceBuiltin, tool); err == nil {
		t.Fatal("expected an error for a Name/Describe mismatch")
	}
}

func TestRegisterCollisionNamesBothSources(t *testing.T) {
	r := registry.BuildRegistry(10)
	mustRegister(t, r, registry.SourceBuiltin, "fs.read")

	err := r.Register(registry.SourceMCP, buildFakeTool("fs.read"))
	if err == nil {
		t.Fatal("expected a collision error")
	}
	for _, want := range []string{"fs.read", string(registry.SourceBuiltin), string(registry.SourceMCP)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	tool, _ := r.Lookup("fs.read")
	if tool.Describe().Name != "fs.read" {
		t.Fatal("original registration was overwritten")
	}
}

func TestNamespacedNamesFromDifferentSourcesCoexist(t *testing.T) {
	r := registry.BuildRegistry(10)
	mustRegister(t, r, registry.SourceBuiltin, "fs.read")
	mustRegister(t, r, registry.SourceMCP, "mcp.fs.read")

	for _, name := range []string{"fs.read", "mcp.fs.read"} {
		if _, isFound := r.Lookup(name); !isFound {
			t.Errorf("Lookup(%q) not found", name)
		}
	}
}

func TestLookupReportsMissingTools(t *testing.T) {
	r := registry.BuildRegistry(10)
	if _, isFound := r.Lookup("fs.read"); isFound {
		t.Fatal("expected a miss on an empty registry")
	}
}

func TestListFiltersBySourceAndPrefix(t *testing.T) {
	r := registry.BuildRegistry(10)
	mustRegister(t, r, registry.SourceBuiltin, "fs.write")
	mustRegister(t, r, registry.SourceBuiltin, "fs.read")
	mustRegister(t, r, registry.SourceBuiltin, "exec")
	mustRegister(t, r, registry.SourceMCP, "mcp.github.issues")

	assertNames(t, r.List(registry.Filter{}), "exec", "fs.read", "fs.write", "mcp.github.issues")
	assertNames(t, r.List(registry.Filter{Source: registry.SourceMCP}), "mcp.github.issues")
	assertNames(t, r.List(registry.Filter{NamePrefix: "fs."}), "fs.read", "fs.write")
	assertNames(t, r.List(registry.Filter{Source: registry.SourceMCP, NamePrefix: "fs."}))
}

func TestPruneRespectsBudgetAndMaxExposed(t *testing.T) {
	r := registry.BuildRegistry(3)
	for _, name := range []string{"e", "d", "c", "b", "a"} {
		mustRegister(t, r, registry.SourceBuiltin, name)
	}
	ctx := context.Background()

	assertNames(t, r.Prune(ctx, 0, ""), "a", "b", "c")
	assertNames(t, r.Prune(ctx, 2, ""), "a", "b")
	assertNames(t, r.Prune(ctx, 100, ""), "a", "b", "c")
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

func TestConcurrentLookupsAreSafe(t *testing.T) {
	r := registry.BuildRegistry(10)
	mustRegister(t, r, registry.SourceBuiltin, "fs.read")

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if _, isFound := r.Lookup("fs.read"); !isFound {
					t.Error("Lookup lost a registration")
					return
				}
				_ = r.List(registry.Filter{})
			}
		}()
	}
	wg.Wait()
}
