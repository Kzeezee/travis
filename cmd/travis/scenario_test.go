package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/eval/scenario"
	"github.com/Kzeezee/travis/internal/providers/cassette"
	"github.com/Kzeezee/travis/internal/providers/openaicompat"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/security/policy"
	"github.com/Kzeezee/travis/internal/security/redact"
)

// TestRegressionScenarios is the 6A regression gate: every scenario in regression.toml must
// run through the real assembleChatSession wiring and satisfy its assertions.
func TestRegressionScenarios(t *testing.T) {
	suite := loadScenarioSuite(t, filepath.Join("..", "..", "testdata", "scenarios", "regression.toml"))
	for _, sc := range suite.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			result, err := runScenario(t, sc)
			if err != nil {
				t.Fatal(err)
			}
			if err := scenario.CheckAssertions(sc, result); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestKnownBadScenariosFail is the harness's self-test (6A "done when"): a scenario with a
// deliberately wrong assertion must still run cleanly but report its assertions as failed,
// which is what proves CheckAssertions is actually checking something.
func TestKnownBadScenariosFail(t *testing.T) {
	suite := loadScenarioSuite(t, filepath.Join("..", "..", "testdata", "scenarios", "regression-known-bad.toml"))
	for _, sc := range suite.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			result, err := runScenario(t, sc)
			if err != nil {
				t.Fatal(err)
			}
			if err := scenario.CheckAssertions(sc, result); err == nil {
				t.Fatalf("scenario %q was expected to fail its assertions but passed", sc.Name)
			}
		})
	}
}

func loadScenarioSuite(t *testing.T, path string) scenario.Suite {
	t.Helper()
	suite, err := scenario.LoadSuite(path)
	if err != nil {
		t.Fatal(err)
	}
	return suite
}

// runScenario replays sc against the real wiring: a scratch copy of its fixture, the real
// broker, and either a recorded cassette or a fixed script standing in for the model.
func runScenario(t *testing.T, sc scenario.Scenario) (scenario.Result, error) {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", sc.Fixture))
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.CopyFS(workspace, os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}

	maxSteps := sc.MaxSteps
	if maxSteps == 0 {
		maxSteps = e2eMaxSteps
	}
	paths := config.PathSet{WorkspaceDir: workspace, StateDir: t.TempDir(), RuntimeDir: buildShortRuntimeDir(t)}
	cfg := &config.Config{Brain: config.BrainCfg{MaxSteps: maxSteps}, Tools: config.ToolsCfg{MaxExposed: 60}}
	var diagnostics strings.Builder
	bus := buildSessionBus(&diagnostics)
	remote, _ := startTestSessionBroker(t, paths, &diagnostics, broker.RemoteOptions{Events: bus})

	model := sc.Model
	if model == "" {
		model = "scripted-model"
	}
	var provider core.Provider
	if sc.Cassette != "" {
		path := filepath.Join("..", "..", "testdata", "cassettes", sc.Cassette+".jsonl")
		provider = openaicompat.BuildProvider("http://cassette.invalid/v1", "", cassette.BuildClient(t, path), core.SystemClock{})
	} else {
		provider = buildScriptedProviderFromSteps(t, sc.Steps)
	}

	session, err := assembleChatSession(cfg, paths, testSessionID, provider, remote, remote, remote, nil, bus, redact.New(), model, &diagnostics, policy.Policy{}, nil)
	if err != nil {
		return scenario.Result{}, err
	}
	events, _ := session.bus.Subscribe(nil)

	var runErr error
	for _, turn := range sc.Turns {
		if err := session.conversation.RunAssistantTurn(context.Background(), turn); err != nil {
			runErr = err
			break
		}
	}
	session.close()
	if runErr != nil {
		return scenario.Result{}, runErr
	}

	isCalled := map[string]bool{}
	var called []string
	var answer strings.Builder
	for event := range events {
		switch fact := event.(type) {
		case core.ToolCallStarted:
			if !isCalled[fact.Call.Name] {
				isCalled[fact.Call.Name] = true
				called = append(called, fact.Call.Name)
			}
		case core.AssistantTextStreamed:
			answer.WriteString(fact.Text)
		}
	}

	files := make(map[string]string, len(sc.AssertFiles))
	for _, assertFile := range sc.AssertFiles {
		content, _ := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(assertFile.Path)))
		files[assertFile.Path] = string(content)
	}
	return scenario.Result{ToolsCalled: called, Answer: answer.String(), Files: files}, nil
}

// buildScriptedProviderFromSteps turns a scenario's fixed steps into the same scriptedProvider
// e2e_test.go's hand-written tests already use, so the runner needs no live model for a
// scenario with no recorded cassette.
func buildScriptedProviderFromSteps(t *testing.T, steps []scenario.ScriptedStep) *scriptedProvider {
	t.Helper()
	scripted := make([]scriptedStep, len(steps))
	for i, step := range steps {
		step, callID := step, core.ID(fmt.Sprintf("step-%d", i+1))
		scripted[i] = func(t *testing.T, _ core.ProviderRequest) []core.Chunk {
			if step.FinalText != "" {
				return []core.Chunk{{Text: step.FinalText, Finish: core.StopEndTurn}}
			}
			return buildToolCallChunks(callID, step.Tool, step.Arguments)
		}
	}
	return &scriptedProvider{t: t, steps: scripted}
}
