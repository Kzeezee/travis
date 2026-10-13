package terminal

import (
	"context"

	"github.com/Kzeezee/travis/internal/core"
)

func BuildQuestioner(requests chan<- core.QuestionRequest) core.Questioner {
	return channelQuestioner{requests: requests}
}

type channelQuestioner struct {
	requests chan<- core.QuestionRequest
}

func (q channelQuestioner) AskUser(ctx context.Context, question string) (string, error) {
	answers := make(chan string, 1)
	request := core.QuestionRequest{Text: question, Respond: func(answer string) { answers <- answer }}
	select {
	case q.requests <- request:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case answer := <-answers:
		return answer, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
