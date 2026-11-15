package loop_test

import (
	"context"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

func TestContextSnapshotDescribesTheActualLatestProviderRequest(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{buildTextStream(
		core.Chunk{Text: "hello"},
		core.Chunk{Usage: &core.Usage{InputTokens: 37, OutputTokens: 1}, Finish: core.StopEndTurn},
	)}}
	conversation := buildAssistantConversation(t, provider, nil, &recordingSink{})

	if err := conversation.RunAssistantTurn(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	snapshot := conversation.ReportContextSnapshot()
	if snapshot.Model != "test-model" || snapshot.MessageCount == 0 || snapshot.RequestBytes == 0 {
		t.Fatalf("snapshot = %+v, want the sent request's model, messages and bytes", snapshot)
	}
	if !snapshot.HasProviderInputTokens || snapshot.ProviderInputTokens != 37 {
		t.Fatalf("provider usage = %d, %t; want 37, true", snapshot.ProviderInputTokens, snapshot.HasProviderInputTokens)
	}

	conversation.ResetHistory()
	if snapshot := conversation.ReportContextSnapshot(); snapshot.Model != "" {
		t.Fatalf("snapshot after reset = %+v, want empty", snapshot)
	}
}

func TestContextSnapshotDistinguishesMissingProviderUsageFromZero(t *testing.T) {
	for name, usage := range map[string]*core.Usage{"missing": nil, "reported zero": {}} {
		t.Run(name, func(t *testing.T) {
			provider := &fakeProvider{streams: []*fakeStream{buildTextStream(
				core.Chunk{Text: "ok", Usage: usage, Finish: core.StopEndTurn},
			)}}
			conversation := buildAssistantConversation(t, provider, nil, &recordingSink{})
			if err := conversation.RunAssistantTurn(context.Background(), "hi"); err != nil {
				t.Fatal(err)
			}
			snapshot := conversation.ReportContextSnapshot()
			if snapshot.HasProviderInputTokens != (usage != nil) {
				t.Fatalf("HasProviderInputTokens = %t, usage = %#v", snapshot.HasProviderInputTokens, usage)
			}
		})
	}
}
