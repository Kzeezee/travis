package core

import "context"

// Questioner pauses a tool until the person at the frontend answers it.
type Questioner interface {
	AskUser(ctx context.Context, question string) (string, error)
}

type QuestionRequest struct {
	Text    string
	Respond func(string)
}
