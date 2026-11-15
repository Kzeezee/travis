package terminal

import (
	"context"
	"image/color"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Kzeezee/travis/internal/core"
)

func TestStartupMascotSettlesIntoSixSecondBlinkTimer(t *testing.T) {
	m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{ModelName: "model"})
	m.startupFrames, m.isMascotTickArmed = startupFrameCount, true

	for range startupFrameCount {
		next, cmd := m.Update(mascotTickMsg{})
		m = next.(chatModel)
		if m.startupFrames > 0 && cmd == nil {
			t.Fatal("startup animation stopped before its final frame")
		}
	}
	if !m.isMascotTickArmed || m.mascotTickInterval != mascotBlinkInterval || m.shouldAnimateMascot() {
		t.Fatalf("post-startup timer = armed %t, interval %s; want six-second idle blink", m.isMascotTickArmed, m.mascotTickInterval)
	}
}

func TestIdleMascotBlinksBrieflyEverySixSeconds(t *testing.T) {
	m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{ModelName: "model"})
	m.startupFrames, m.isMascotTickArmed, m.mascotTickInterval = 0, false, 0
	m.mascotFrame = 2

	cmd := m.armMascotAnimation(nil)
	if cmd == nil || !m.isMascotTickArmed || m.mascotTickInterval != mascotBlinkInterval {
		t.Fatalf("idle timer = armed %t, interval %s", m.isMascotTickArmed, m.mascotTickInterval)
	}
	next, _ := m.Update(mascotTickMsg{})
	m = next.(chatModel)
	if !m.isMascotBlinking || m.mascotFrame != 1 || m.mascotTickInterval != mascotBlinkFrameInterval {
		t.Fatalf("closed blink = blinking %t, frame %d, next %s", m.isMascotBlinking, m.mascotFrame, m.mascotTickInterval)
	}
	next, _ = m.Update(mascotTickMsg{})
	m = next.(chatModel)
	if m.isMascotBlinking || m.mascotFrame != 2 || m.mascotTickInterval != mascotBlinkInterval {
		t.Fatalf("open blink = blinking %t, frame %d, next %s", m.isMascotBlinking, m.mascotFrame, m.mascotTickInterval)
	}
}

func TestTaskAnimationSupersedesPendingIdleBlink(t *testing.T) {
	m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{ModelName: "model"})
	m.startupFrames = 0
	m.isMascotTickArmed = true
	m.mascotTickInterval = mascotBlinkInterval
	m.mascotTickGeneration = 4
	m.cancelTurn = func() {}

	if cmd := m.armMascotAnimation(nil); cmd == nil || m.mascotTickInterval != mascotFrameInterval || m.mascotTickGeneration != 5 {
		t.Fatalf("active timer = cmd %v, interval %s, generation %d", cmd != nil, m.mascotTickInterval, m.mascotTickGeneration)
	}
	frame := m.mascotFrame
	next, _ := m.Update(mascotTickMsg{generation: 4})
	m = next.(chatModel)
	if m.mascotFrame != frame || !m.isMascotTickArmed {
		t.Fatal("stale idle blink disrupted the active animation")
	}
}

func TestReducedMotionKeepsMascotStatic(t *testing.T) {
	m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{
		ModelName: "model", ReducedMotion: true,
	})
	m.cancelTurn = func() {}
	m.dictation = dictationRecording
	m.settleFrames = settleFrameCount
	if m.shouldAnimateMascot() || m.isMascotTickArmed {
		t.Fatal("reduced motion enabled animation")
	}
}

func TestMascotStatesStayCompact(t *testing.T) {
	m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{ModelName: "model"})
	m.startupFrames = 0
	for _, state := range []struct {
		name  string
		apply func(*chatModel)
	}{
		{"idle", func(*chatModel) {}},
		{"thinking", func(m *chatModel) { m.cancelTurn = func() {} }},
		{"listening", func(m *chatModel) { m.dictation = dictationRecording }},
		{"success", func(m *chatModel) { m.settleFrames, m.lastTurnOutcome = 1, turnSucceeded }},
		{"failure", func(m *chatModel) { m.settleFrames, m.lastTurnOutcome = 1, turnFailed }},
	} {
		t.Run(state.name, func(t *testing.T) {
			candidate := m
			state.apply(&candidate)
			if got := candidate.renderCompactMascot(); len([]rune(got)) != 1 {
				t.Fatalf("compact mascot %q uses %d runes", got, len([]rune(got)))
			}
		})
	}
}

func TestRunningTurnDiamondGlowsAndDims(t *testing.T) {
	m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{ModelName: "model"})
	m.cancelTurn = func() {}

	got := make([]string, 4)
	for frame := range got {
		m.mascotFrame = frame
		got[frame] = m.renderCompactMascot()
	}
	if strings.Join(got, "") != "◊◆◆◆" {
		t.Fatalf("thinking frames = %q, want dim, bright, glow, bright", got)
	}
}

func TestWelcomeOnlyAdvertisesDictationWhenAvailable(t *testing.T) {
	theme := buildTerminalTheme(themeDark)
	withoutVoice := renderWelcome(80, 16, "model", false, theme, 0)
	withVoice := renderWelcome(80, 16, "model", true, theme, 0)
	if strings.Contains(withoutVoice, "ctrl+t") {
		t.Fatal("welcome advertised dictation while voice input was unavailable")
	}
	if !strings.Contains(withVoice, "ctrl+t to talk") {
		t.Fatal("welcome omitted the available dictation shortcut")
	}
}

func TestBeaconBuddyPulsesAndBlinksWithoutChangingShape(t *testing.T) {
	theme := buildTerminalTheme(themeDark)
	idle := renderBeaconBuddy(theme, 0)
	blink := renderBeaconBuddy(theme, 1)
	if !strings.Contains(idle, "▄█▄") || !strings.Contains(idle, "██████████") || !strings.Contains(blink, "▄▓▄") || !strings.Contains(blink, "▄▄") {
		t.Fatalf("beacon buddy frames are missing their beacon or face:\n%s\n%s", idle, blink)
	}
	if lipgloss.Width(idle) != lipgloss.Width(blink) {
		t.Fatalf("beacon buddy width changed from %d to %d", lipgloss.Width(idle), lipgloss.Width(blink))
	}
}

func TestThemePreviewChangesTheSemanticPalette(t *testing.T) {
	m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{ModelName: "model"})
	darkMark := m.theme.assistantLabel.Render("◆")
	m, _ = selectTheme(m, themeLight)
	if m.themePreference != themeLight || m.theme.name != themeLight {
		t.Fatalf("theme = %q/%q, want light", m.themePreference, m.theme.name)
	}
	if lightMark := m.theme.assistantLabel.Render("◆"); lightMark == darkMark {
		t.Fatal("light preview kept the dark palette")
	}

	m, _ = selectTheme(m, "unknown")
	if !slices.ContainsFunc(m.transcript.notices, func(notice string) bool {
		return strings.Contains(notice, "Unknown theme")
	}) {
		t.Fatal("unknown theme did not produce a notice")
	}
}

func TestAutoThemeFollowsTerminalBackground(t *testing.T) {
	m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{
		ModelName: "model", Theme: themeAuto,
	})
	next, _ := m.Update(tea.BackgroundColorMsg{Color: color.White})
	m = next.(chatModel)
	if m.theme.name != themeLight {
		t.Fatalf("light background selected %q theme", m.theme.name)
	}
	next, _ = m.Update(tea.BackgroundColorMsg{Color: color.Black})
	if got := next.(chatModel).theme.name; got != themeDark {
		t.Fatalf("dark background selected %q theme", got)
	}
}

func TestMonoThemeEmitsNoColourSequences(t *testing.T) {
	theme := buildTerminalTheme(themeMono)
	rendered := theme.assistantLabel.Render("◆") + theme.error.Render("error") + theme.selectedRow.Render("row")
	if strings.Contains(rendered, "[38;") || strings.Contains(rendered, "[48;") {
		t.Fatalf("mono theme emitted colour: %q", rendered)
	}
}

func TestApprovalPausesAndThenRearmsActiveAnimation(t *testing.T) {
	m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{ModelName: "model"})
	m.startupFrames, m.isMascotTickArmed = 0, false
	m.cancelTurn = func() {}
	m.pendingApproval = &core.ApprovalRequest{Respond: func(core.ApprovalDecision) {}}
	if m.shouldAnimateMascot() {
		t.Fatal("approval should hold the mascot still")
	}

	next, _ := m.handleApprovalKey(tea.KeyPressMsg{Code: 'y'})
	m = next.(chatModel)
	if !m.isMascotTickArmed {
		t.Fatal("answering an approval did not resume the active turn animation")
	}
}
