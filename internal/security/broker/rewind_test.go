package broker_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/state/blobs"
	"github.com/Kzeezee/travis/internal/state/session/filestore"
)

func inTurn(turn core.ID) context.Context {
	return core.WithTurnID(context.Background(), turn)
}

func writeInTurn(t *testing.T, enforced *broker.PolicyEnforcedBroker, turn core.ID, path, content string) {
	t.Helper()
	if _, err := enforced.WriteFile(inTurn(turn), path, []byte(content)); err != nil {
		t.Fatalf("WriteFile(%s) in %s: %v", path, turn, err)
	}
}

func reportWorkspaceFile(t *testing.T, workspace, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return "<absent>"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// buildEditedSession is a session whose turn-1 edits a.txt and creates new.txt.
func buildEditedSession(t *testing.T) (*broker.PolicyEnforcedBroker, string, *memoryLog, *memoryBackups) {
	t.Helper()
	records, log, backups := buildTestRecords()
	enforced, workspace, _ := buildRecordedSessionBroker(t, "default", records)
	writeWorkspaceFile(t, workspace, "a.txt", "a0")
	writeInTurn(t, enforced, "turn-1", "a.txt", "a1")
	writeInTurn(t, enforced, "turn-1", "new.txt", "n1")
	return enforced, workspace, log, backups
}

func TestRewindDoesWhatEachModeSays(t *testing.T) {
	for _, tc := range []struct {
		mode        core.RewindMode
		wantA       string
		wantNew     string
		wantEntries []core.SessionEntryKind
	}{
		{core.RewindFilesAndConversation, "a0", "<absent>", []core.SessionEntryKind{core.EntryFilesReverted, core.EntryRewound}},
		{core.RewindConversationOnly, "a1", "n1", []core.SessionEntryKind{core.EntryRewound}},
		{core.RewindFilesOnly, "a0", "<absent>", []core.SessionEntryKind{core.EntryFilesReverted}},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			enforced, workspace, log, _ := buildEditedSession(t)
			report, err := enforced.Rewind(context.Background(), "turn-1", tc.mode)
			if err != nil {
				t.Fatal(err)
			}
			if got := reportWorkspaceFile(t, workspace, "a.txt"); got != tc.wantA {
				t.Errorf("a.txt = %q, want %q", got, tc.wantA)
			}
			if got := reportWorkspaceFile(t, workspace, "new.txt"); got != tc.wantNew {
				t.Errorf("new.txt = %q, want %q", got, tc.wantNew)
			}
			if got := log.reportKinds()[2:]; !slices.Equal(got, tc.wantEntries) {
				t.Errorf("logged after the writes = %v, want %v", got, tc.wantEntries)
			}
			for _, entry := range log.entries[2:] {
				if entry.Author != core.AuthorBroker || entry.Rewind.FromTurn != "turn-1" || entry.Rewind.Mode != tc.mode {
					t.Errorf("rewind entry %+v %+v, want the broker's, from turn-1 in %s mode", entry, entry.Rewind, tc.mode)
				}
			}
			if tc.mode.IsRevertingFiles() {
				want := core.FileReverts{Restored: []string{"a.txt"}, Deleted: []string{"new.txt"}}
				if report.Mode != tc.mode || !slices.Equal(report.Files.Restored, want.Restored) || !slices.Equal(report.Files.Deleted, want.Deleted) {
					t.Errorf("report = %+v, want %+v in workspace paths", report, want)
				}
			}
		})
	}
}

// A file someone changed after the agent wrote it is left alone, and only that file.
func TestRewindLeavesAFileEditedSinceAndRestoresTheRest(t *testing.T) {
	enforced, workspace, log, _ := buildEditedSession(t)
	writeWorkspaceFile(t, workspace, "a.txt", "edited by hand")

	report, err := enforced.Rewind(context.Background(), "turn-1", core.RewindFilesOnly)
	if err != nil {
		t.Fatal(err)
	}
	if got := reportWorkspaceFile(t, workspace, "a.txt"); got != "edited by hand" {
		t.Fatalf("a.txt = %q, want the hand edit kept", got)
	}
	if got := reportWorkspaceFile(t, workspace, "new.txt"); got != "<absent>" {
		t.Fatalf("new.txt = %q, want it removed", got)
	}
	if len(report.Files.Skipped) != 1 || report.Files.Skipped[0].Path != "a.txt" || report.Files.Skipped[0].Reason == "" {
		t.Fatalf("skipped = %+v, want a.txt with its reason", report.Files.Skipped)
	}
	reverted := findEntries(log, core.EntryFilesReverted)[0].Rewind.ChangeIDs
	if len(reverted) != 1 || reverted[0] != log.entries[1].ID {
		t.Fatalf("reverted change ids = %v, want only new.txt's %q: a skipped change is not reverted", reverted, log.entries[1].ID)
	}
}

func TestRewindLeavesAFileThatHadNoBackup(t *testing.T) {
	records, _, backups := buildTestRecords()
	backups.maxBytes = 4
	enforced, workspace, _ := buildRecordedSessionBroker(t, "default", records)
	writeWorkspaceFile(t, workspace, "big.txt", "more than four bytes")
	var required *core.ApprovalRequiredError
	if _, err := enforced.WriteFile(inTurn("turn-1"), "big.txt", []byte("x")); !errors.As(err, &required) {
		t.Fatalf("WriteFile = %v, want an irreversible ask", err)
	}
	if err := enforced.ApproveOnce(inTurn("turn-1"), core.Capability{Kind: required.Operation.Kind, Scope: required.Operation.Target}); err != nil {
		t.Fatal(err)
	}
	writeInTurn(t, enforced, "turn-1", "big.txt", "x")

	report, err := enforced.Rewind(context.Background(), "turn-1", core.RewindFilesOnly)
	if err != nil {
		t.Fatal(err)
	}
	if got := reportWorkspaceFile(t, workspace, "big.txt"); got != "x" || len(report.Files.Skipped) != 1 {
		t.Fatalf("big.txt = %q, skipped = %+v; want it left as written and reported", got, report.Files.Skipped)
	}
}

// Files are restored before the rewind is logged: a log that fails afterwards cannot leave a
// rewind on record whose files were never put back.
func TestRewindRestoresFilesBeforeLoggingIt(t *testing.T) {
	enforced, workspace, log, _ := buildEditedSession(t)
	log.appendErr = errStoreBroken

	if _, err := enforced.Rewind(context.Background(), "turn-1", core.RewindFilesAndConversation); !errors.Is(err, errStoreBroken) {
		t.Fatalf("Rewind = %v, want the log's failure", err)
	}
	if got := reportWorkspaceFile(t, workspace, "a.txt"); got != "a0" {
		t.Fatalf("a.txt = %q, want it already restored when the log failed", got)
	}
}

func TestRewindRefusesWhatIsNotInTheSession(t *testing.T) {
	enforced, _, _, _ := buildEditedSession(t)
	if _, err := enforced.Rewind(context.Background(), "turn-9", core.RewindFilesOnly); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Rewind(unknown turn) = %v, want ErrInvalid", err)
	}
	if _, err := enforced.Rewind(context.Background(), "turn-1", "everything"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Rewind(unknown mode) = %v, want ErrInvalid", err)
	}
}

func TestListTurnsLeavesOutRewoundTurns(t *testing.T) {
	enforced, _, _, _ := buildEditedSession(t)
	writeInTurn(t, enforced, "turn-2", "b.txt", "b1")
	if _, err := enforced.Rewind(context.Background(), "turn-2", core.RewindConversationOnly); err != nil {
		t.Fatal(err)
	}
	turns, err := enforced.ListTurns(context.Background())
	if err != nil || len(turns) != 1 || turns[0].Turn != "turn-1" {
		t.Fatalf("ListTurns = %+v, %v; want only turn-1", turns, err)
	}
}

func TestRemoteBrokerRewindsThroughTheWire(t *testing.T) {
	enforced, workspace, _, _ := buildEditedSession(t)
	remote, _ := startPipedBroker(t, enforced)

	report, err := remote.Rewind(context.Background(), "turn-1", core.RewindFilesAndConversation)
	if err != nil {
		t.Fatal(err)
	}
	if report.FromTurn != "turn-1" || !slices.Equal(report.Files.Restored, []string{"a.txt"}) || !slices.Equal(report.Files.Deleted, []string{"new.txt"}) {
		t.Fatalf("report = %+v", report)
	}
	if got := reportWorkspaceFile(t, workspace, "a.txt"); got != "a0" {
		t.Fatalf("a.txt = %q", got)
	}
}

// Rewinding from any turn puts every file back exactly as it was before that turn began, however
// the turns before and after it wrote, created and rewrote files (§6.4: reversible means it undoes).
func TestRewindRestoresTheWorkspaceAsItWasBeforeTheTurn(t *testing.T) {
	names := []string{"a.txt", "b.txt", "sub/c.txt"}
	random := rand.New(rand.NewPCG(31, 7))
	rewound := 0
	for trial := range 25 {
		base, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		workspace := filepath.Join(base, "workspace")
		writeWorkspaceFile(t, workspace, "a.txt", "seed")
		backups, err := blobs.Open(filepath.Join(base, "blobs"), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		records := broker.SessionRecords{Log: &memoryLog{}, Backups: backups, IDs: &core.SequentialIDGenerator{Prefix: "e"}}
		enforced := buildWorkspaceBroker(t, base, workspace, records)

		turnCount := 2 + random.IntN(4)
		before := make([]map[string]string, turnCount)
		for turn := range turnCount {
			before[turn] = captureFiles(t, workspace, names)
			for _, name := range names {
				if random.IntN(2) == 0 {
					writeInTurn(t, enforced, core.ID(fmt.Sprintf("t%d", turn)), name, fmt.Sprintf("trial %d turn %d %s", trial, turn, name))
				}
			}
		}

		from := random.IntN(turnCount)
		if _, err := enforced.Rewind(context.Background(), core.ID(fmt.Sprintf("t%d", from)), core.RewindFilesOnly); err != nil {
			if !errors.Is(err, core.ErrInvalid) || hasAnyWrite(before, from) {
				t.Fatalf("trial %d: Rewind(t%d) = %v", trial, from, err)
			}
			continue // that turn wrote nothing, so it is not in the session
		}
		if got := captureFiles(t, workspace, names); !mapsEqual(got, before[from]) {
			t.Fatalf("trial %d: after rewinding from t%d the workspace is %v, want %v", trial, from, got, before[from])
		}
		rewound++
	}
	if rewound < 15 {
		t.Fatalf("only %d of 25 trials rewound anything; the property was barely exercised", rewound)
	}
}

// hasAnyWrite reports whether turn from left any file different from how the next turn found it;
// a turn that wrote nothing logged nothing, so a rewind cannot name it.
func hasAnyWrite(before []map[string]string, from int) bool {
	return from+1 < len(before) && !mapsEqual(before[from], before[from+1])
}

func buildWorkspaceBroker(t *testing.T, base, workspace string, records broker.SessionRecords) *broker.PolicyEnforcedBroker {
	t.Helper()
	paths := config.PathSet{WorkspaceDir: workspace, StateDir: filepath.Join(base, "state")}
	sessionPolicy, err := broker.BuildSessionPolicy("default", broker.SessionRoots{Paths: paths, Home: base, Temp: filepath.Join(base, "temp")})
	if err != nil {
		t.Fatal(err)
	}
	return broker.BuildPolicyEnforcedBroker(workspace, sessionPolicy, records, core.SystemClock{})
}

func captureFiles(t *testing.T, workspace string, names []string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, name := range names {
		files[name] = reportWorkspaceFile(t, workspace, name)
	}
	return files
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

const rewindCrashHelperEnv = "TRAVIS_BROKER_REWIND_CRASH_HELPER"

// stallBeforeLoggedRevert is a session log that, when asked to log a files_reverted entry, reports
// ready and stalls first, so the parent can kill -9 the broker after files are restored but
// before the rewind is on record.
type stallBeforeLoggedRevert struct{ *filestore.Store }

func (l stallBeforeLoggedRevert) Append(entries ...core.SessionEntry) error {
	if entries[0].Kind == core.EntryFilesReverted {
		fmt.Println("ready")
		time.Sleep(time.Hour)
	}
	return l.Store.Append(entries...)
}

// TestRewindCrashHelper is the broker TestCrashBetweenRestoringAndLoggingNeverOverwrites kills.
func TestRewindCrashHelper(t *testing.T) {
	if os.Getenv(rewindCrashHelperEnv) == "" {
		t.Skip("runs only as a crash-test child process")
	}
	base, workspace := os.Getenv("BASE"), os.Getenv("WORKSPACE")
	sessionLog, err := filestore.Open(filepath.Join(base, "sessions"), "crash")
	if err != nil {
		t.Fatal(err)
	}
	backups, err := blobs.Open(filepath.Join(base, "blobs"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	records := broker.SessionRecords{Log: stallBeforeLoggedRevert{sessionLog}, Backups: backups, IDs: &core.SequentialIDGenerator{Prefix: "e"}}
	enforced := buildWorkspaceBroker(t, base, workspace, records)
	writeInTurn(t, enforced, "turn-1", "a.txt", "written by the agent")
	if _, err := enforced.Rewind(context.Background(), "turn-1", core.RewindFilesOnly); err != nil {
		t.Fatal(err)
	}
	t.Fatal("the rewind finished; the crash helper should have stalled before logging it")
}

// A kill -9 after files are restored but before the rewind is logged leaves the files restored
// and the changes still on record. Rewinding again then finds the files no longer match what the
// agent wrote and leaves them alone: a crash can lose the record of a rewind, never data.
func TestCrashBetweenRestoringAndLoggingNeverOverwrites(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(base, "workspace")
	writeWorkspaceFile(t, workspace, "a.txt", "original")
	cmd := buildCrashHelper(t, "^TestRewindCrashHelper$", rewindCrashHelperEnv, "BASE="+base, "WORKSPACE="+workspace)
	runCrashHelperUntilReady(t, cmd)

	if got := reportWorkspaceFile(t, workspace, "a.txt"); got != "original" {
		t.Fatalf("a.txt = %q after the crash, want it already restored", got)
	}
	sessionLog, err := filestore.Open(filepath.Join(base, "sessions"), "crash")
	if err != nil {
		t.Fatal(err)
	}
	defer sessionLog.Close()
	backups, err := blobs.Open(filepath.Join(base, "blobs"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	records := broker.SessionRecords{Log: sessionLog, Backups: backups, IDs: &core.SequentialIDGenerator{Prefix: "after"}}
	enforced := buildWorkspaceBroker(t, base, workspace, records)
	writeWorkspaceFile(t, workspace, "a.txt", "the user's own edit")

	report, err := enforced.Rewind(context.Background(), "turn-1", core.RewindFilesOnly)
	if err != nil {
		t.Fatal(err)
	}
	if got := reportWorkspaceFile(t, workspace, "a.txt"); got != "the user's own edit" || len(report.Files.Skipped) != 1 {
		t.Fatalf("a.txt = %q, skipped = %+v; want the user's edit kept and the file reported", got, report.Files.Skipped)
	}
}
