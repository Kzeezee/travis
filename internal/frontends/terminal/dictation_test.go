package terminal

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Kzeezee/travis/internal/core"
)

var (
	pushToTalkPress   = tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	pushToTalkRelease = tea.KeyReleaseMsg{Code: 't', Mod: tea.ModCtrl}
)

// fakeDictation records how often the microphone was opened and closed.
type fakeDictation struct {
	transcript       string
	starts, finishes int
}

func (d *fakeDictation) start(context.Context) (func() (string, error), error) {
	d.starts++
	return func() (string, error) {
		d.finishes++
		return d.transcript, nil
	}, nil
}

func buildDictationModel(t *testing.T, transcript string) (chatModel, *fakeDictation, *int) {
	t.Helper()
	dictation := &fakeDictation{transcript: transcript}
	turns := 0
	m := buildTestModel(ChatActions{
		RunTurn:        func(context.Context, string) error { turns++; return nil },
		StartDictation: dictation.start,
	})
	return m, dictation, &turns
}

// runDictationCmd runs one command and feeds its message back, as the Bubble Tea runtime would.
func runDictationCmd(t *testing.T, m chatModel, cmd tea.Cmd) chatModel {
	t.Helper()
	if cmd == nil {
		return m
	}
	m, _ = updateModel(m, cmd())
	return m
}

func pressPushToTalk(t *testing.T, m chatModel, msg tea.Msg) chatModel {
	t.Helper()
	m, cmd := updateModel(m, msg)
	return runDictationCmd(t, m, cmd)
}

func TestToggleDictationFillsTheInputAndNeverSends(t *testing.T) {
	m, dictation, turns := buildDictationModel(t, "rename the notes file")
	m.input.SetValue("please")

	m = pressPushToTalk(t, m, pushToTalkPress)
	if m.dictation != dictationRecording {
		t.Fatalf("dictation = %v after the first press, want recording", m.dictation)
	}
	m = pressPushToTalk(t, m, pushToTalkPress)
	m = runDictationCmd(t, m, nil)

	if got := m.input.Value(); got != "please rename the notes file" {
		t.Fatalf("input = %q, want the transcript appended", got)
	}
	if *turns != 0 {
		t.Fatal("dictation sent the prompt; only enter may do that")
	}
	if dictation.finishes != 1 || m.dictation != dictationIdle {
		t.Fatalf("finishes = %d, state = %v; want the microphone closed", dictation.finishes, m.dictation)
	}
}

func TestHoldDictationStopsOnRelease(t *testing.T) {
	m, _, _ := buildDictationModel(t, "hello")
	m, _ = updateModel(m, tea.KeyboardEnhancementsMsg{Flags: 0b11})

	m = pressPushToTalk(t, m, pushToTalkPress)
	m = pressPushToTalk(t, m, tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl, IsRepeat: true})
	if m.dictation != dictationRecording {
		t.Fatal("a held key's repeats stopped the recording")
	}
	m = pressPushToTalk(t, m, pushToTalkRelease)

	if m.input.Value() != "hello" {
		t.Fatalf("input = %q, want the transcript after release", m.input.Value())
	}
}

// §5.10, D-10: an open approval takes every key, so push-to-talk cannot even start.
func TestOpenApprovalIgnoresPushToTalk(t *testing.T) {
	m, dictation, _ := buildDictationModel(t, "yes")
	m.pendingApproval = &core.ApprovalRequest{Respond: func(core.ApprovalDecision) {}}

	m = pressPushToTalk(t, m, pushToTalkPress)

	if dictation.starts != 0 || m.pendingApproval == nil {
		t.Fatalf("starts = %d; the microphone opened under an approval", dictation.starts)
	}
}

// An approval arriving mid-recording closes the microphone and drops what was said.
func TestApprovalMidRecordingDiscardsTheDictation(t *testing.T) {
	m, dictation, _ := buildDictationModel(t, "yes")
	m = pressPushToTalk(t, m, pushToTalkPress)

	m, cmd := updateModel(m, approvalRequestedMsg{request: core.ApprovalRequest{Respond: func(core.ApprovalDecision) {}}})
	m = runDictationCmd(t, m, cmd)

	if dictation.finishes != 1 {
		t.Fatal("the microphone stayed open under an approval")
	}
	if m.input.Value() != "" || m.pendingApproval == nil {
		t.Fatalf("input = %q; a transcript reached the screen while an approval was open", m.input.Value())
	}
}

func TestPushToTalkIsAnOrdinaryKeyWhenVoiceIsOff(t *testing.T) {
	m := buildTestModel(ChatActions{RunTurn: func(context.Context, string) error { return nil }})

	m, _ = updateModel(m, pushToTalkPress)

	if m.dictation != dictationIdle {
		t.Fatalf("dictation = %v with voice input off", m.dictation)
	}
}
