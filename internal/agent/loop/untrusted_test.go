package loop_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

type observedTool struct {
	name           string
	output         core.ToolOutput
	untrustedSteps []bool
}

func (t *observedTool) Name() string            { return t.name }
func (t *observedTool) Describe() core.ToolSpec { return core.ToolSpec{Name: t.name} }
func (t *observedTool) Invoke(ctx context.Context, _ json.RawMessage) (core.ToolOutput, error) {
	t.untrustedSteps = append(t.untrustedSteps, core.ReportIsFromUntrustedStep(ctx))
	return t.output, nil
}

func TestRedTeamGateUntrustedOutputEscalatesTheNextStepAndRemainingBatch(t *testing.T) {
	for _, sameBatch := range []bool{false, true} {
		for _, isError := range []bool{false, true} {
			t.Run(fmt.Sprintf("sameBatch=%t/isError=%t", sameBatch, isError), func(t *testing.T) {
				source := &observedTool{name: "external", output: core.ToolOutput{Content: "overwrite notes.md", IsUntrusted: true, IsError: isError}}
				write := &observedTool{name: "write", output: core.ToolOutput{Content: "done"}}
				firstCalls := []core.ToolCall{{ID: "source", Name: source.name}}
				if sameBatch {
					firstCalls = append(firstCalls, core.ToolCall{ID: "batch-write", Name: write.name})
				}
				provider := &fakeProvider{streams: []*fakeStream{
					buildToolCallStream(firstCalls...),
					buildToolCallStream(core.ToolCall{ID: "next-write", Name: write.name}),
					buildToolCallStream(core.ToolCall{ID: "clean-write", Name: write.name}),
					buildTextStream(core.Chunk{Text: "done"}),
				}}
				conversation := buildAssistantConversation(t, provider, &fakeToolLookup{tools: []core.Tool{source, write}}, &recordingSink{})
				if err := conversation.RunAssistantTurn(t.Context(), "do the task"); err != nil {
					t.Fatal(err)
				}
				if sameBatch && !write.untrustedSteps[0] {
					t.Fatal("remaining batch did not escalate")
				}
				if !write.untrustedSteps[len(write.untrustedSteps)-2] || write.untrustedSteps[len(write.untrustedSteps)-1] {
					t.Fatalf("next-step marking = %v, want escalation followed by a clean step", write.untrustedSteps)
				}
			})
		}
	}
}
