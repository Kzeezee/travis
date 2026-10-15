// Package scenario is the regression harness's data half (6A): a TOML-described fixture,
// scripted turns, and assertions, kept decision-only so it never has to know about the
// broker, the provider, or the agent loop. cmd/travis owns running one against the real
// wiring (it is the only package that can reach assembleChatSession).
package scenario

import (
	"fmt"
	"io"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// AssertFile checks that a file under the scenario's workspace contains a substring once the
// scripted turns finish.
type AssertFile struct {
	Path     string `toml:"path"`
	Contains string `toml:"contains"`
}

// ScriptedStep is one deterministic model turn for a scenario with no recorded cassette:
// exactly one of Tool or FinalText is set.
type ScriptedStep struct {
	Tool      string `toml:"tool"`
	Arguments string `toml:"arguments"`
	FinalText string `toml:"final_text"`
}

// Scenario is a fixture directory, how the model is played (a cassette or a fixed script),
// the user turns to run, and what must be true once they finish. Exactly one of Cassette or
// Steps is set, the same way an MCP server is trusted by exact (command, args): each
// scenario commits to one source of model output, never a mix.
type Scenario struct {
	Name     string `toml:"name"`
	Fixture  string `toml:"fixture"`
	Cassette string `toml:"cassette"`
	Model    string `toml:"model"`
	MaxSteps int    `toml:"max_steps"`
	// ToolsMaxExposed overrides tools.max_exposed for this scenario's session; 0 falls back
	// to the runner's own default. Lets a scenario prove pruning behavior (3C) end to end.
	ToolsMaxExposed      int            `toml:"tools_max_exposed"`
	Steps                []ScriptedStep `toml:"step"`
	Turns                []string       `toml:"turns"`
	AssertToolsCalled    []string       `toml:"assert_tools_called"`
	AssertToolsNotCalled []string       `toml:"assert_tools_not_called"`
	// AssertToolsOffered checks the tool specs actually sent to the model on its first
	// call (what Registry.Prune returned), not which tools it went on to call — the two
	// can diverge, since Lookup finds a tool by name regardless of whether Prune offered
	// it. Only the scripted runner (not a cassette replay) can observe this.
	AssertToolsOffered   []string     `toml:"assert_tools_offered"`
	AssertAnswerContains []string     `toml:"assert_answer_contains"`
	AssertFiles          []AssertFile `toml:"assert_files"`

	// AssertPromptLacks checks the first call's system prompt; scripted runner only, like
	// AssertToolsOffered.
	AssertPromptLacks []string `toml:"assert_prompt_lacks"`
}

// Suite is every scenario in one TOML file.
type Suite struct {
	Scenarios []Scenario `toml:"scenario"`
}

// DecodeSuite reads and validates a scenario suite from r.
func DecodeSuite(r io.Reader) (Suite, error) {
	var suite Suite
	if err := toml.NewDecoder(r).DisallowUnknownFields().Decode(&suite); err != nil {
		return Suite{}, fmt.Errorf("decode scenario suite: %w", err)
	}
	for _, sc := range suite.Scenarios {
		if err := validateScenario(sc); err != nil {
			return Suite{}, err
		}
	}
	return suite, nil
}

// LoadSuite reads and validates the scenario suite at path.
func LoadSuite(path string) (Suite, error) {
	file, err := os.Open(path)
	if err != nil {
		return Suite{}, fmt.Errorf("scenario suite %s: %w", path, err)
	}
	defer file.Close()
	suite, err := DecodeSuite(file)
	if err != nil {
		return Suite{}, fmt.Errorf("scenario suite %s: %w", path, err)
	}
	return suite, nil
}

// validateScenario rejects a malformed scenario before anything tries to run it: a missing
// name, fixture or turns list, both or neither of cassette/steps, and a step that sets both
// or neither of tool/final_text.
func validateScenario(sc Scenario) error {
	if sc.Name == "" {
		return fmt.Errorf("a scenario is missing a name")
	}
	if sc.Fixture == "" {
		return fmt.Errorf("%q: missing fixture", sc.Name)
	}
	if len(sc.Turns) == 0 {
		return fmt.Errorf("%q: missing turns", sc.Name)
	}
	if (sc.Cassette != "") == (len(sc.Steps) > 0) {
		return fmt.Errorf("%q: exactly one of cassette or step entries is required", sc.Name)
	}
	for i, step := range sc.Steps {
		if (step.Tool != "") == (step.FinalText != "") {
			return fmt.Errorf("%q: step %d must set exactly one of tool or final_text", sc.Name, i)
		}
	}
	return nil
}
