package builtin

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const askUserDescription = "Ask the user a question and wait for their typed answer. Use it when you cannot finish " +
	"the request without a choice or fact only the user has, such as which of several matching items they mean; " +
	"ask here instead of ending your reply with a question. Look things up with the other tools first and never " +
	"ask for what they can find. List every candidate you found, as found, without ruling any out or adding caveats."

const askUserSchema = `{"type":"object","properties":{"question":{"type":"string","description":"A question for the user"}},"required":["question"],"additionalProperties":false}`

type AskUserTool struct {
	questioner core.Questioner
}

func BuildAskUserTool(questioner core.Questioner) *AskUserTool {
	return &AskUserTool{questioner: questioner}
}

func (t *AskUserTool) Name() string { return "ask_user" }

func (t *AskUserTool) Describe() core.ToolSpec {
	return core.ToolSpec{Name: t.Name(), Description: askUserDescription, InputSchema: json.RawMessage(askUserSchema)}
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
