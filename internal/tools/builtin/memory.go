package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/memory"
)

const memorySchema = `{"type":"object","properties":{"action":{"type":"string","enum":["read","replace"]},"content":{"type":"string","description":"Complete replacement for the current note section; remove old notes before adding when full"}},"required":["action"],"additionalProperties":false}`

type MemoryTool struct {
	store core.MemoryStore
	path  string
}

func BuildMemoryTool(store core.MemoryStore, path string) *MemoryTool {
	return &MemoryTool{store: store, path: path}
}

func (t *MemoryTool) Name() string { return "memory" }

func (t *MemoryTool) Describe() core.ToolSpec {
	return core.ToolSpec{
		Name: t.Name(), Description: "Read or replace bounded markdown memory notes. Untrusted-derived notes are kept in a separate delimited section.",
		InputSchema:  json.RawMessage(memorySchema),
		Capabilities: []core.Capability{{Kind: core.CapabilityFSRead, Scope: t.path}, {Kind: core.CapabilityFSWrite, Scope: t.path}},
	}
}

func (t *MemoryTool) Invoke(ctx context.Context, arguments json.RawMessage) (core.ToolOutput, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolOutput{}, err
	}
	var args struct {
		Action  string  `json:"action"`
		Content *string `json:"content"`
	}
	if err := decodeStrictArgs(arguments, &args); err != nil {
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": err.Error()}), nil
	}
	if t.store == nil {
		return buildErrorOutput("memory_unavailable", nil), nil
	}
	switch args.Action {
	case "read":
		if args.Content != nil {
			return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": "read takes no content"}), nil
		}
		content, err := t.store.ReadMemory(ctx)
		if err != nil {
			return t.reportFailure(ctx, err)
		}
		return core.ToolOutput{Content: content, Display: "read memory", IsUntrusted: memory.ReportHasUntrustedNotes(content)}, nil
	case "replace":
		if args.Content == nil {
			return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": "replace needs content"}), nil
		}
		if err := t.store.ReplaceMemory(ctx, *args.Content); err != nil {
			return t.reportFailure(ctx, err)
		}
		return core.ToolOutput{Content: strings.TrimSpace(*args.Content), Display: "updated memory"}, nil
	default:
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": "action must be read or replace"}), nil
	}
}

func (t *MemoryTool) reportFailure(ctx context.Context, err error) (core.ToolOutput, error) {
	if ctx.Err() != nil {
		return core.ToolOutput{}, ctx.Err()
	}
	code := "memory_failed"
	if errors.Is(err, core.ErrDenied) {
		code = "memory_denied"
	} else if errors.Is(err, core.ErrInvalid) {
		code = "memory_rejected"
	}
	return buildErrorOutput(code, map[string]any{"detail": err.Error()}), nil
}
