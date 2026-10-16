package main

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

// buildCompleteDeps is the smallest set assembleChatSession accepts; each subtest removes one.
func buildCompleteDeps(t *testing.T) chatSessionDeps {
	t.Helper()
	return chatSessionDeps{
		Cfg:      &config.Config{},
		Paths:    config.PathSet{WorkspaceDir: "/w", StateDir: "/s"},
		Session:  "session-1",
		Provider: &scriptedProvider{},
		Model:    "test-model",
		Files:    broker.BuildLocalBroker(t.TempDir(), core.SystemClock{}),
		Bus:      core.BuildEventBus(nil, 1),

		Diagnostics: io.Discard,
	}
}

// A struct of dependencies trades the compiler's "not enough arguments" for a nil at run time,
// so every required one must be named by an error rather than found as a panic.
func TestAssembleChatSessionNamesAMissingDependency(t *testing.T) {
	removals := map[string]func(*chatSessionDeps){
		"cfg":           func(d *chatSessionDeps) { d.Cfg = nil },
		"workspace dir": func(d *chatSessionDeps) { d.Paths.WorkspaceDir = "" },
		"state dir":     func(d *chatSessionDeps) { d.Paths.StateDir = "" },
		"session id":    func(d *chatSessionDeps) { d.Session = "" },
		"provider":      func(d *chatSessionDeps) { d.Provider = nil },
		"model":         func(d *chatSessionDeps) { d.Model = "" },
		"file broker":   func(d *chatSessionDeps) { d.Files = nil },
		"event bus":     func(d *chatSessionDeps) { d.Bus = nil },
		"diagnostics":   func(d *chatSessionDeps) { d.Diagnostics = nil },
	}
	for name, remove := range removals {
		t.Run(name, func(t *testing.T) {
			deps := buildCompleteDeps(t)
			remove(&deps)

			session, err := assembleChatSession(deps)

			if session != nil {
				t.Fatal("a session was assembled from incomplete dependencies")
			}
			if !errors.Is(err, core.ErrInvalid) || !strings.Contains(err.Error(), name) {
				t.Fatalf("error = %v, want it to name the missing %s as invalid", err, name)
			}
		})
	}
}

// The optional dependencies are the ones a test with no human and no broker process leaves
// out; leaving every one of them out must still assemble.
func TestAssembleChatSessionAcceptsTheOptionalDependenciesBeingAbsent(t *testing.T) {
	session, err := assembleChatSession(buildCompleteDeps(t))
	if err != nil {
		t.Fatalf("assembleChatSession = %v, want a session", err)
	}
	defer session.close()

	if names := session.reportToolNames(); len(names) == 0 {
		t.Fatal("no tools were registered")
	}
}
