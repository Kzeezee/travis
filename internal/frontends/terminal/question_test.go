package terminal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
)

func TestQuestionOperationWaitsForTheTypedAnswer(t *testing.T) {
	requests := make(chan core.QuestionRequest)
	questioner := BuildQuestioner(requests)
	answered := make(chan string, 1)
	go func() {
		answer, _ := questioner.AskUser(context.Background(), "Which day?")
		answered <- answer
	}()
	request := <-requests
	if request.Text != "Which day?" {
		t.Fatalf("question = %q", request.Text)
	}
	select {
	case answer := <-answered:
		t.Fatalf("returned before the user answered: %q", answer)
	default:
	}
	request.Respond("Friday")
	select {
	case answer := <-answered:
		if answer != "Friday" {
			t.Fatalf("answer = %q", answer)
		}
	case <-time.After(time.Second):
		t.Fatal("question did not return the answer")
	}
}

func TestQuestionOperationStopsWhenTheTurnIsCancelled(t *testing.T) {
	questions := make(chan core.QuestionRequest)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildQuestioner(questions).AskUser(ctx, "Which day?"); !errors.Is(err, context.Canceled) {
		t.Fatalf("AskUser error = %v", err)
	}
}
