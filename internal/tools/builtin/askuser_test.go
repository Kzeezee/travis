package builtin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/tools/builtin"
	"github.com/Kzeezee/travis/internal/tools/tooltest"
)

type answeringQuestioner struct {
	question string
}

func (q *answeringQuestioner) AskUser(_ context.Context, question string) (string, error) {
	q.question = question
	return "  Tuesday  ", nil
}

func TestAskUserReturnsTheTypedAnswerAsAToolResult(t *testing.T) {
	questioner := &answeringQuestioner{}
	tool := builtin.BuildAskUserTool(questioner)
	output, err := tool.Invoke(context.Background(), json.RawMessage(`{"question":"When should I send it?"}`))
	if err != nil || output.IsError || output.Content != "Tuesday" || questioner.question != "When should I send it?" {
		t.Fatalf("Invoke = %+v, %v; question = %q", output, err, questioner.question)
	}
}

func TestAskUserMeetsTheToolContract(t *testing.T) {
	var questioner core.Questioner = &answeringQuestioner{}
	tooltest.Run(t, builtin.BuildAskUserTool(questioner))
}
