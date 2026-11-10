package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

// runBrokeredSession starts the real broker for start, logs a user and an assistant message per
// prompt, and reports the history the session was restored with.
func runBrokeredSession(t *testing.T, paths config.PathSet, start sessionStart, prompts ...string) []core.SessionEntry {
	t.Helper()
	var diagnostics strings.Builder
	sessionBroker, err := startSessionBroker(paths, start, config.SessionCfg{SnapshotMaxBytes: 1 << 20}, &diagnostics, broker.RemoteOptions{Events: buildSessionBus(io.Discard)})
	if err != nil {
		t.Fatalf("start session broker: %v (diagnostics: %s)", err, &diagnostics)
	}
	defer func() {
		if err := sessionBroker.Close(); err != nil {
			t.Errorf("session broker: %v", err)
		}
	}()
	var restored []core.SessionEntry
	if start.reportIsContinuing() {
		if restored, _, err = loadRestoredSession(paths, start.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, prompt := range prompts {
		ask := core.Message{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: prompt}}}
		answer := core.Message{Role: core.RoleAssistant, Parts: []core.Part{core.TextPart{Text: "re: " + prompt}}}
		turn := core.ID("turn-" + prompt)
		if err := sessionBroker.Remote.AppendEntries(context.Background(), []core.SessionEntry{
			{Kind: core.EntryMessage, Turn: turn, Message: &ask},
			{Kind: core.EntryMessage, Turn: turn, Message: &answer},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return restored
}

// A session quit and resumed keeps its history and goes on appending to the same log; a fork
// from it starts a new log with the chosen turns and leaves the original byte for byte.
func TestSessionResumesWithItsHistoryAndAForkLeavesTheOriginalUnchanged(t *testing.T) {
	travisHome := t.TempDir()
	t.Setenv("TRAVIS_HOME", travisHome)
	paths := config.PathSet{WorkspaceDir: t.TempDir(), RuntimeDir: buildShortRuntimeDir(t), StateDir: filepath.Join(travisHome, "state")}

	runBrokeredSession(t, paths, sessionStart{ID: "01ORIGINAL"}, "one", "two")

	resumed, err := resolveSessionStart(paths, latestSessionAlias, "", 0)
	if err != nil || resumed.ID != "01ORIGINAL" {
		t.Fatalf("resolve --resume last = %+v, %v", resumed, err)
	}
	if history := runBrokeredSession(t, paths, resumed, "three"); len(history) != 4 {
		t.Fatalf("resumed with %d entries, want both earlier turns' four messages", len(history))
	}
	originalPath := filepath.Join(paths.StateDir, "sessions", "01ORIGINAL.jsonl")
	original, err := os.ReadFile(originalPath)
	if err != nil || strings.Count(string(original), "\n") != 6 {
		t.Fatalf("resumed log has %d lines (%v), want the third turn appended to the first two", strings.Count(string(original), "\n"), err)
	}

	forked, err := resolveSessionStart(paths, "", "01ORIGINAL", 1)
	if err != nil || forked.ID == "01ORIGINAL" || forked.ForkFrom != "01ORIGINAL" {
		t.Fatalf("resolve --fork = %+v, %v", forked, err)
	}
	history := runBrokeredSession(t, paths, forked, "branch")
	if len(history) != 2 || history[0].Turn != "turn-one" {
		t.Fatalf("fork started with %+v, want only the first turn", history)
	}
	if after, _ := os.ReadFile(originalPath); string(after) != string(original) {
		t.Fatal("forking changed the original session's log")
	}
}

func TestResolveSessionStartRefusesWhatCannotBeResumed(t *testing.T) {
	paths := config.PathSet{StateDir: t.TempDir()}
	for name, flags := range map[string][3]string{
		"unknown session": {"01NOPE", "", ""},
		"both flags":      {"a", "b", ""},
		"--at alone":      {"", "", "2"},
	} {
		forkTurns := 0
		if flags[2] != "" {
			forkTurns = 2
		}
		if _, err := resolveSessionStart(paths, flags[0], flags[1], forkTurns); err == nil {
			t.Errorf("%s: resolveSessionStart accepted it", name)
		}
	}
}

func TestPickSessionIDListsNewestFirst(t *testing.T) {
	rows := []sessionRow{{ID: "01OLDER", Line: "older"}, {ID: "01NEWER", Line: "newer"}}
	for answer, want := range map[string]core.ID{"\n": "01NEWER", "2\n": "01OLDER", "1": "01NEWER"} {
		if got, err := pickSessionID(rows, strings.NewReader(answer), io.Discard); err != nil || got != want {
			t.Errorf("answer %q picked %q, %v; want %q", answer, got, err, want)
		}
	}
	for _, answer := range []string{"9\n", "x\n", ""} {
		if _, err := pickSessionID(rows, strings.NewReader(answer), io.Discard); err == nil {
			t.Errorf("answer %q was accepted", answer)
		}
	}
}

func TestPickSessionIDOffersOnlyTheNewestFive(t *testing.T) {
	var rows []sessionRow
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		rows = append(rows, sessionRow{ID: core.ID(name), Line: "line-" + name})
	}
	var out strings.Builder
	if got, err := pickSessionID(rows, strings.NewReader("5\n"), &out); err != nil || got != "c" {
		t.Fatalf("answer 5 picked %q, %v; want the fifth newest, c", got, err)
	}
	if strings.Contains(out.String(), "line-b") || !strings.Contains(out.String(), "travis sessions") {
		t.Fatalf("picker showed %q; want five sessions and a pointer to travis sessions", out.String())
	}
	if _, err := pickSessionID(rows, strings.NewReader("6\n"), io.Discard); err == nil {
		t.Fatal("answer 6 was accepted beyond the five offered")
	}
	out.Reset()
	pickSessionID(rows[:2], strings.NewReader("\n"), &out)
	if strings.Contains(out.String(), "travis sessions") {
		t.Fatal("pointed to travis sessions with nothing older to show")
	}
}

func TestAllowBareResumeOnlyRewritesAValuelessFlag(t *testing.T) {
	for _, tc := range []struct{ args, want string }{
		{"--resume", "--resume="},
		{"--resume --model x", "--resume= --model x"},
		{"--resume abc", "--resume abc"},
		{"--model x", "--model x"},
	} {
		if got := strings.Join(allowBareResume(strings.Fields(tc.args)), " "); got != tc.want {
			t.Errorf("allowBareResume(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
