package voiceout

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
)

// recordingSpeaker is the null player: it speaks nothing and records each line it was given.
type recordingSpeaker struct {
	spoken chan string
}

func (s recordingSpeaker) Speak(_ context.Context, text string) error {
	s.spoken <- text
	return nil
}

type discardLogger struct{}

func (discardLogger) LogDebugMsg(string, ...any) {}
func (discardLogger) LogInfoMsg(string, ...any)  {}
func (discardLogger) LogWarnMsg(string, ...any)  {}
func (discardLogger) LogErrorMsg(string, ...any) {}

func startRecordingNarrator(t *testing.T) (*Narrator, chan string) {
	t.Helper()
	spoken := make(chan string, queuedLineLimit)
	narrator := StartNarrator(recordingSpeaker{spoken: spoken}, discardLogger{})
	t.Cleanup(narrator.Close)
	return narrator, spoken
}

func receiveSpoken(t *testing.T, spoken chan string) string {
	t.Helper()
	select {
	case line := <-spoken:
		return line
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was spoken")
		return ""
	}
}

func TestNarratorSpeaksRequestedSpeechInOrderAndIgnoresOtherEvents(t *testing.T) {
	narrator, spoken := startRecordingNarrator(t)
	events := make(chan core.Event, 3)
	events <- core.SpeechRequested{Text: "first"}
	events <- core.AssistantTextStreamed{Text: "shown only"}
	events <- core.SpeechRequested{Text: "second"}
	close(events)

	narrator.SpeakRequestedSpeech(events)

	got := []string{receiveSpoken(t, spoken), receiveSpoken(t, spoken)}
	if !slices.Equal(got, []string{"first", "second"}) {
		t.Fatalf("spoken = %q", got)
	}
}

func TestNarratorCutsOverlongSpeech(t *testing.T) {
	narrator, spoken := startRecordingNarrator(t)

	narrator.Say(strings.Repeat("é", maxSpokenRunes+50))

	if got := []rune(receiveSpoken(t, spoken)); len(got) != maxSpokenRunes {
		t.Fatalf("spoke %d runes, want %d", len(got), maxSpokenRunes)
	}
}

type fixedApprover struct{ decision core.ApprovalDecision }

func (a fixedApprover) RequestApproval(context.Context, core.Operation) (core.ApprovalDecision, error) {
	return a.decision, nil
}

type fixedQuestioner struct{ answer string }

func (q fixedQuestioner) AskUser(context.Context, string) (string, error) { return q.answer, nil }

// D-10: the alert is spoken, but the decision is still whatever the keyboard answered.
func TestWrappedApproverAnnouncesAndKeepsTheKeyboardAnswer(t *testing.T) {
	narrator, spoken := startRecordingNarrator(t)

	decision, err := WrapApprover(fixedApprover{decision: core.ApprovalDenied}, narrator).RequestApproval(context.Background(), core.Operation{})

	if err != nil || decision != core.ApprovalDenied {
		t.Fatalf("decision = %v, err = %v; want the wrapped approver's denial", decision, err)
	}
	if line := receiveSpoken(t, spoken); line != approvalAlert {
		t.Fatalf("spoke %q, want %q", line, approvalAlert)
	}
}

func TestWrappedQuestionerAnnouncesAndKeepsTheTypedAnswer(t *testing.T) {
	narrator, spoken := startRecordingNarrator(t)

	answer, err := WrapQuestioner(fixedQuestioner{answer: "typed"}, narrator).AskUser(context.Background(), "q?")

	if err != nil || answer != "typed" {
		t.Fatalf("answer = %q, err = %v", answer, err)
	}
	if line := receiveSpoken(t, spoken); line != questionAlert {
		t.Fatalf("spoke %q, want %q", line, questionAlert)
	}
}

// A nil approver means nobody can approve; wrapping it must not turn that into a panic.
func TestWrappingNothingStaysNothing(t *testing.T) {
	narrator, _ := startRecordingNarrator(t)

	if WrapApprover(nil, narrator) != nil || WrapQuestioner(nil, narrator) != nil {
		t.Fatal("wrapping a nil port returned a non-nil one")
	}
}

func TestEveryShippedOSHasASpeechProgram(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if len(listSpeechPrograms(goos)) == 0 {
			t.Errorf("%s has no speech program", goos)
		}
	}
}

const helperOutputEnv = "TRAVIS_VOICEOUT_HELPER_OUTPUT"

// TestHelperSpeechProgram is not a test: it stands in for say/espeak, recording argv and stdin.
func TestHelperSpeechProgram(t *testing.T) {
	output := os.Getenv(helperOutputEnv)
	if output == "" {
		t.Skip("only runs as a child process")
	}
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	record := strings.Join(os.Args, "\x00") + "\n--stdin--\n" + string(stdin)
	if err := os.WriteFile(output, []byte(record), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

// §5.11 boundary rule: model text reaches the speech program on stdin, never in argv, where a
// leading "-" would turn it into a flag.
func TestProgramSpeakerSendsTextOnStdinNotArgv(t *testing.T) {
	output := filepath.Join(t.TempDir(), "record")
	t.Setenv(helperOutputEnv, output)
	text := "--version; rm -rf / $(whoami)"
	speaker := programSpeaker{path: os.Args[0], args: []string{"-test.run=^TestHelperSpeechProgram$"}}

	if err := speaker.Speak(context.Background(), text); err != nil {
		t.Fatal(err)
	}

	record, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	argv, stdin, _ := strings.Cut(string(record), "\n--stdin--\n")
	if strings.Contains(argv, text) {
		t.Fatalf("model text reached argv: %q", argv)
	}
	if stdin != text {
		t.Fatalf("stdin = %q, want %q", stdin, text)
	}
}

func TestProgramSpeakerReportsAFailingProgram(t *testing.T) {
	speaker := programSpeaker{path: os.Args[0], args: []string{"-test.run=^TestHelperSpeechProgram$"}}
	t.Setenv(helperOutputEnv, filepath.Join(t.TempDir(), "missing-dir", "record"))

	err := speaker.Speak(context.Background(), "hi")

	var exitErr interface{ ExitCode() int }
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v, want the program's exit status", err)
	}
}
