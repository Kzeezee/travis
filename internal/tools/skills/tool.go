package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const openToolSchema = `{"type":"object","properties":{"name":{"type":"string","description":"A skill name from the index"}},"required":["name"],"additionalProperties":false}`

// openTool is the level-2 half of progressive disclosure (§5.8): the index already in
// the system prompt costs ~30 tokens per skill, and this returns one skill's full body
// only when the model actually asks for it.
type openTool struct {
	catalog *Catalog
}

// BuildOpenTool exposes catalog's skills on demand as "skill_open" (D-31: single
// underscore, core-tool style — "__" is reserved for mcp__<server>__<tool>).
func BuildOpenTool(catalog *Catalog) core.Tool {
	return &openTool{catalog: catalog}
}

func (t *openTool) Name() string { return "skill_open" }

func (t *openTool) Describe() core.ToolSpec {
	return core.ToolSpec{
		Name:        t.Name(),
		Description: "Return one skill's full instructions by name, from the index already in the system prompt.",
		InputSchema: json.RawMessage(openToolSchema),
	}
}

// Invoke never returns a Go error for a bad or unknown name — the model can see the
// result and retry with a different name, same as every other builtin tool's
// "normal business result, not a Go error" convention (Code-Conventions.md).
func (t *openTool) Invoke(ctx context.Context, arguments json.RawMessage) (core.ToolOutput, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolOutput{}, err
	}

	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil || strings.TrimSpace(args.Name) == "" {
		return core.ToolOutput{IsError: true, Content: `{"error":"invalid_arguments"}`, Display: "skill_open: invalid arguments"}, nil
	}

	body, ok := t.catalog.open(args.Name)
	if !ok {
		return core.ToolOutput{
			IsError: true,
			Content: fmt.Sprintf(`{"error":"unknown_skill","name":%q}`, args.Name),
			Display: "skill not found",
		}, nil
	}
	return core.ToolOutput{Content: body, Display: "opened skill " + args.Name}, nil
}
