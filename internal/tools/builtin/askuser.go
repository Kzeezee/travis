package builtin

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const askUserSchema = `{"type":"object","properties":{"question":{"type":"string","description":"A question for the user"}},"required":["question"],"additionalProperties":false}`

type AskUserTool struct {
	questioner core.Questioner
}

func BuildAskUserTool(questioner core.Questioner) *AskUserTool {
	return &AskUserTool{questioner: questioner}
}

func (t *AskUserTool) Name() string { return "ask_user" }

func (t *AskUserTool) Describe() core.ToolSpec {
	return core.ToolSpec{Name: t.Name(), Description: "Ask the user a question and wait for their typed answer.", InputSchema: json.RawMessage(askUserSchema)}
}

func (t *AskUserTool) Invoke(ctx context.Context, arguments json.RawMessage) (core.ToolOutput, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolOutput{}, err
	}
	var args struct {
		Question string `json:"question"`
	}
	if err := decodeStrictArgs(arguments, &args); err != nil {
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": err.Error()}), nil
	}
	question := strings.TrimSpace(args.Question)
	if question == "" || len(question) > 4096 {
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": "question must be 1..4096 bytes"}), nil
	}
	if t.questioner == nil {
		return buildErrorOutput("question_unavailable", nil), nil
	}
	answer, err := t.questioner.AskUser(ctx, question)
	if err != nil {
		if ctx.Err() != nil {
			return core.ToolOutput{}, ctx.Err()
		}
		return buildErrorOutput("question_failed", map[string]any{"detail": err.Error()}), nil
	}
	return core.ToolOutput{Content: strings.TrimSpace(answer), Display: "user answered"}, nil
}
