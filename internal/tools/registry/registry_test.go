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
