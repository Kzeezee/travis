// Package tooltest is the contract every core.Tool must meet, whatever its source (DESIGN §5.5).
package tooltest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	cancelDeadline = 100 * time.Millisecond
	nestingDepth   = 10_000
)

type invokeOutcome struct {
	err      error
	panicked any
}

// Run checks the tool contract. The fourth §5.5 rule, "never performs an effect when the
// broker denies", is not checkable until the Phase 2 policy and broker exist.
func Run(t *testing.T, tool core.Tool) {
	t.Helper()
	t.Run("DescribeIsStable", func(t *testing.T) { checkDescribeIsStable(t, tool) })
	t.Run("NameMatchesDescribe", func(t *testing.T) { checkNameMatchesDescribe(t, tool) })
	t.Run("SchemaIsAJSONObject", func(t *testing.T) { checkSchemaIsAJSONObject(t, tool) })
	t.Run("AdversarialArgumentsNeverPanic", func(t *testing.T) { checkAdversarialArguments(t, tool) })
	t.Run("CancelledContextReturnsPromptly", func(t *testing.T) { checkCancelledContextReturnsPromptly(t, tool) })
}

func checkDescribeIsStable(t *testing.T, tool core.Tool) {
	first, second := tool.Describe(), tool.Describe()
	if !bytes.Equal(first.InputSchema, second.InputSchema) {
		t.Fatalf("schema changed between calls:\n%s\n%s", first.InputSchema, second.InputSchema)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Describe() changed between calls:\n%+v\n%+v", first, second)
	}
}

func checkNameMatchesDescribe(t *testing.T, tool core.Tool) {
	name := tool.Name()
	if name == "" {
		t.Fatal("Name() is empty")
	}
	if described := tool.Describe().Name; described != name {
		t.Fatalf("Name() = %q, Describe().Name = %q", name, described)
	}
}

func checkSchemaIsAJSONObject(t *testing.T, tool core.Tool) {
	schema := tool.Describe().InputSchema
	var object map[string]any
	if err := json.Unmarshal(schema, &object); err != nil || object == nil {
		t.Fatalf("InputSchema is not a JSON object (%v): %s", err, schema)
	}
}

// bad arguments are a value result the model corrects, so a Go error here is a contract breach too
func checkAdversarialArguments(t *testing.T, tool core.Tool) {
	for _, input := range buildAdversarialArguments() {
		t.Run(summarizeArguments(input), func(t *testing.T) {
			outcome := invokeRecoveringPanic(context.Background(), tool, input)
			if outcome.panicked != nil {
				t.Fatalf("Invoke panicked: %v", outcome.panicked)
			}
			if outcome.err != nil {
				t.Fatalf("Invoke returned a Go error for bad arguments: %v", outcome.err)
			}
		})
	}
}

func checkCancelledContextReturnsPromptly(t *testing.T, tool core.Tool) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan invokeOutcome, 1)
	go func() { done <- invokeRecoveringPanic(ctx, tool, json.RawMessage(`{}`)) }()

	select {
	case outcome := <-done:
		if outcome.panicked != nil {
			t.Fatalf("Invoke panicked on a cancelled context: %v", outcome.panicked)
		}
	case <-time.After(cancelDeadline):
		t.Fatalf("Invoke did not return within %v of an already-cancelled context", cancelDeadline)
	}
}

func invokeRecoveringPanic(ctx context.Context, tool core.Tool, arguments json.RawMessage) (outcome invokeOutcome) {
	defer func() { outcome.panicked = recover() }()
	_, outcome.err = tool.Invoke(ctx, arguments)
	return outcome
}

func buildAdversarialArguments() []json.RawMessage {
	inputs := []string{
		``,
		`null`,
		`[]`,
		`"str"`,
		`42`,
		`{}`,
		`{"path":`,
		`{"path":"../../etc/passwd"}`,
		"{\"path\":\"\xff\xfe\"}",
		strings.Repeat(`{"a":`, nestingDepth) + `1` + strings.Repeat(`}`, nestingDepth),
		strings.Repeat(`[`, nestingDepth) + strings.Repeat(`]`, nestingDepth),
	}
	arguments := make([]json.RawMessage, len(inputs))
	for i, input := range inputs {
		arguments[i] = json.RawMessage(input)
	}
	return arguments
}

func summarizeArguments(arguments json.RawMessage) string {
	const maxShown = 32
	if len(arguments) <= maxShown {
		return fmt.Sprintf("%q", arguments)
	}
	return fmt.Sprintf("%q...(%d bytes)", arguments[:maxShown], len(arguments))
}
