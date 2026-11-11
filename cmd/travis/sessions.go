package main

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
	"github.com/Kzeezee/travis/internal/id"
	"github.com/Kzeezee/travis/internal/state/session"
	"github.com/Kzeezee/travis/internal/state/session/filestore"
)

// latestSessionAlias stands for the most recent session in `travis chat --resume`.
const latestSessionAlias = "last"

const sessionPromptPreviewRunes = 60

// pickerSessionLimit is how many of the newest sessions a bare --resume offers.
const pickerSessionLimit = 5

// sessionStart is which log a chat's broker keeps: a fresh one, a resumed one, or a fork seeded
// from another session's log through ForkTurns turns.
type sessionStart struct {
	ID        core.ID
	ForkFrom  core.ID
	ForkTurns int
	isResumed bool
}

// reportIsContinuing reports whether the session starts from an existing log.
func (s sessionStart) reportIsContinuing() bool {
	return s.ForkFrom != "" || s.isResumed
}

// resolveSessionStart turns the --resume and --fork flags into the session to open. A resumed
// session must already exist, so a typo is an error rather than a fresh empty session.
func resolveSessionStart(paths config.PathSet, resume, fork string, forkTurns int) (sessionStart, error) {
	newID := func() core.ID { return id.BuildULIDGenerator(core.SystemClock{}).GenerateID() }
	switch {
	case resume != "" && fork != "":
		return sessionStart{}, fmt.Errorf("--resume and --fork are mutually exclusive")
	case resume == "" && fork == "":
		if forkTurns != 0 {
			return sessionStart{}, fmt.Errorf("--at needs --fork")
		}
		return sessionStart{ID: newID()}, nil
	case forkTurns < 0:
		return sessionStart{}, fmt.Errorf("--at must be zero (every turn) or a positive turn count")
	}
	existing, err := resolveLoggedSessionID(paths, resume+fork)
	if err != nil {
		return sessionStart{}, err
	}
	if resume != "" {
		return sessionStart{ID: existing, isResumed: true}, nil
	}
	return sessionStart{ID: newID(), ForkFrom: existing, ForkTurns: forkTurns}, nil
}

// resolveLoggedSessionID finds name among the logged sessions; "last" is the newest.
func resolveLoggedSessionID(paths config.PathSet, name string) (core.ID, error) {
	ids, err := filestore.ListSessionIDs(buildSessionsDir(paths))
	if err != nil {
		return "", err
	}
	if name == latestSessionAlias && len(ids) > 0 {
		return ids[len(ids)-1], nil
	}
	if !slices.Contains(ids, core.ID(name)) {
		return "", fmt.Errorf("no session %q (travis sessions lists them)", name)
	}
	return core.ID(name), nil
}

// loadRestoredSession reads the session's log back, once its broker has opened or seeded it, for
// the conversation and the transcript to continue from.
func loadRestoredSession(paths config.PathSet, sessionID core.ID) ([]core.SessionEntry, []terminal.RestoredMessage, error) {
	entries, err := filestore.ReadEntries(buildSessionsDir(paths), sessionID)
	if err != nil {
		return nil, nil, err
	}
	var history []terminal.RestoredMessage
	for _, entry := range session.SelectVisibleEntries(entries) {
		if entry.Kind == core.EntryMessage && entry.Message != nil {
			history = append(history, terminal.RestoredMessage{Turn: entry.Turn, Message: *entry.Message})
		}
	}
	return entries, history, nil
}

// runSessions lists past sessions, oldest first, for picking one to resume or fork.
func runSessions(out io.Writer) error {
	paths, err := config.BuildCurrentPathSet()
	if err != nil {
		return err
	}
	rows, err := buildSessionRows(buildSessionsDir(paths))
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no sessions yet (travis chat starts one)")
		return nil
	}
	for _, row := range rows {
		fmt.Fprintln(out, row.Line)
	}
	fmt.Fprintln(out, "\nresume: travis chat --resume [id|last]   fork: travis chat --fork <id> [--at <turns>]")
	return nil
}

// sessionRow is one logged session as `travis sessions` and the --resume picker show it.
type sessionRow struct {
	ID   core.ID
	Line string
}

// buildSessionRows describes each logged session, oldest first.
func buildSessionRows(dir string) ([]sessionRow, error) {
	ids, err := filestore.ListSessionIDs(dir)
	if err != nil {
		return nil, err
	}
	rows := make([]sessionRow, 0, len(ids))
	for _, sessionID := range ids {
		entries, err := filestore.ReadEntries(dir, sessionID)
		if err != nil {
			rows = append(rows, sessionRow{ID: sessionID, Line: fmt.Sprintf("%s  unreadable: %v", sessionID, err)})
			continue
		}
		turns := session.BuildTurnSummaries(entries)
		started, prompt := "-", ""
		if len(turns) > 0 {
			started, prompt = turns[0].At.Local().Format("2006-01-02 15:04"), turns[0].Prompt
		}
		rows = append(rows, sessionRow{ID: sessionID, Line: fmt.Sprintf("%s  %s  %3d turns  %s", sessionID, started, len(turns), buildPromptPreview(prompt))})
	}
	return rows, nil
}

// pickSessionID lists rows newest first, numbered, and reads which one to resume; Enter picks the newest.
func pickSessionID(rows []sessionRow, in io.Reader, out io.Writer) (core.ID, error) {
	if len(rows) == 0 {
		return "", fmt.Errorf("no sessions yet (travis chat starts one)")
	}
	hasOlder := len(rows) > pickerSessionLimit
	if hasOlder {
		rows = rows[len(rows)-pickerSessionLimit:]
	}
	for n := range len(rows) {
		fmt.Fprintf(out, "%3d  %s\n", n+1, rows[len(rows)-1-n].Line)
	}
	if hasOlder {
		fmt.Fprintln(out, "older sessions: run travis sessions, then travis chat --resume <id>")
	}
	fmt.Fprint(out, "resume which? [1]: ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("no session picked")
	}
	choice := 1
	if answer := strings.TrimSpace(line); answer != "" {
		if choice, err = strconv.Atoi(answer); err != nil || choice < 1 || choice > len(rows) {
			return "", fmt.Errorf("pick a number from 1 to %d", len(rows))
		}
	}
	return rows[len(rows)-choice].ID, nil
}

// allowBareResume lets `--resume` stand alone, as `--resume=` (pick from a list), since the flag
// package would otherwise take the next flag as its value or fail at the end of args.
func allowBareResume(args []string) []string {
	rewritten := slices.Clone(args)
	for i, arg := range rewritten {
		if arg == "--" {
			break
		}
		isLast := i == len(rewritten)-1
		if (arg == "--resume" || arg == "-resume") && (isLast || strings.HasPrefix(rewritten[i+1], "-")) {
			rewritten[i] = "--resume="
		}
	}
	return rewritten
}

func buildPromptPreview(prompt string) string {
	firstLine, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	if runes := []rune(firstLine); len(runes) > sessionPromptPreviewRunes {
		return string(runes[:sessionPromptPreviewRunes-1]) + "…"
	}
	return firstLine
}

func buildSessionsDir(paths config.PathSet) string {
	return filepath.Join(paths.StateDir, "sessions")
}
