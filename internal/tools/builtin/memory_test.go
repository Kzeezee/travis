package builtin_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/tools/builtin"
	"github.com/Kzeezee/travis/internal/tools/tooltest"
)

type recordingMemoryStore struct {
	content string
	denied  bool
}

func (s *recordingMemoryStore) ReadMemory(context.Context) (string, error) {
	if s.denied {
		return "", core.ErrDenied
	}
	return s.content, nil
}

func (s *recordingMemoryStore) ReplaceMemory(_ context.Context, content string) error {
	if s.denied {
		return core.ErrDenied
	}
	s.content = content
	return nil
}

func TestMemoryToolReadsAndReplacesNotes(t *testing.T) {
	store := &recordingMemoryStore{content: "# Memory\n"}
	tool := builtin.BuildMemoryTool(store, "/state/memory.md")
	for _, tc := range []struct{ args, want string }{
		{`{"action":"read"}`, "# Memory\n"},
		{`{"action":"replace","content":"Call Alice."}`, "Call Alice."},
		{`{"action":"read"}`, "Call Alice."},
	} {
		output, err := tool.Invoke(context.Background(), json.RawMessage(tc.args))
		if err != nil || output.IsError || !strings.Contains(output.Content, tc.want) {
			t.Fatalf("Invoke(%s) = %+v, %v", tc.args, output, err)
		}
	}
	if store.content != "Call Alice." {
		t.Fatalf("stored %q", store.content)
	}
}

func TestMemoryToolDoesNotWriteWhenStoreDenies(t *testing.T) {
	store := &recordingMemoryStore{content: "keep", denied: true}
	tool := builtin.BuildMemoryTool(store, "/state/memory.md")
	output, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"replace","content":"evil"}`))
	if err != nil || !output.IsError || store.content != "keep" {
		t.Fatalf("Invoke = %+v, %v; stored %q", output, err, store.content)
	}
}

func TestMemoryToolMeetsTheToolContract(t *testing.T) {
	tooltest.Run(t, builtin.BuildMemoryTool(&recordingMemoryStore{}, "/state/memory.md"))
}
