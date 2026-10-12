package broker_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/state/blobs"
	"github.com/Kzeezee/travis/internal/state/session/filestore"
)

// §5.15.1: the backup is taken, then the change is logged, and only then does the file change.
func TestWriteBacksUpThenLogsThenWrites(t *testing.T) {
	records, log, backups := buildTestRecords()
	enforced, workspace, _ := buildRecordedSessionBroker(t, "default", records)
	writeWorkspaceFile(t, workspace, "a.txt", "old")
	log.onAppend = func([]core.SessionEntry) {
		if got := readWorkspaceFile(t, workspace, "a.txt"); got != "old" {
			t.Errorf("a.txt was %q when its change was logged, want the old bytes still there", got)
		}
	}

	ctx := core.WithToolCallID(testTurn, "call-1")
	info, err := enforced.WriteFile(ctx, "a.txt", []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if got := readWorkspaceFile(t, workspace, "a.txt"); got != "new" {
		t.Fatalf("a.txt = %q, want the new bytes", got)
	}
	if backups.seen[info.Path] != "old" {
		t.Fatalf("backup saw %q, want the old bytes", backups.seen[info.Path])
	}
	requireEntryKinds(t, log, core.EntryFileChanged)
	entry := log.entries[0]
	want := core.FileChange{Path: info.Path, Before: string(blobs.HashContent([]byte("old"))), After: string(blobs.HashContent([]byte("new")))}
	if entry.Author != core.AuthorBroker || entry.Turn != "turn-1" || entry.Call != "call-1" || entry.ID == "" || *entry.FileChange != want {
		t.Fatalf("file_changed = %+v %+v, want the broker's entry for turn-1/call-1 with %+v", entry, entry.FileChange, want)
	}
}

func TestWriteOfANewFileLogsThatThereWasNothingBefore(t *testing.T) {
	records, log, _ := buildTestRecords()
	enforced, _, _ := buildRecordedSessionBroker(t, "default", records)
	if _, err := enforced.WriteFile(testTurn, "notes/new.md", []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if changes := findEntries(log, core.EntryFileChanged); len(changes) != 1 || changes[0].FileChange.Before != "" {
		t.Fatalf("file_changed = %+v, want one with no backup", changes)
	}
}

func TestWriteIsNotMadeWhenItsBackupOrLogEntryFails(t *testing.T) {
	for name, breakRecords := range map[string]func(*memoryLog, *memoryBackups){
		"backup fails": func(_ *memoryLog, backups *memoryBackups) { backups.snapshotErr = errStoreBroken },
		"log fails":    func(log *memoryLog, _ *memoryBackups) { log.appendErr = errStoreBroken },
	} {
		t.Run(name, func(t *testing.T) {
			records, log, backups := buildTestRecords()
			enforced, workspace, _ := buildRecordedSessionBroker(t, "default", records)
			writeWorkspaceFile(t, workspace, "a.txt", "old")
			breakRecords(log, backups)

			if _, err := enforced.WriteFile(testTurn, "a.txt", []byte("new")); !errors.Is(err, errStoreBroken) {
				t.Fatalf("WriteFile = %v, want the store's failure", err)
			}
			if got := readWorkspaceFile(t, workspace, "a.txt"); got != "old" {
				t.Fatalf("a.txt = %q, want it untouched: an unrecorded write could never be undone", got)
			}
		})
	}
}

func TestWriteOutsideATurnIsRefused(t *testing.T) {
	records, log, _ := buildTestRecords()
	enforced, workspace, _ := buildRecordedSessionBroker(t, "default", records)
	if _, err := enforced.WriteFile(context.Background(), "a.txt", []byte("x")); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("WriteFile outside a turn = %v, want ErrDenied", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "a.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the refused write still created a.txt: %v", err)
	}
	if len(log.entries) != 0 {
		t.Fatalf("session log = %v, want nothing recorded", log.reportKinds())
	}
}

// A refusal is the broker's own fact to record, and policy runs before anything is backed up.
func TestDeniedEffectsAreLoggedAndNeverBackedUp(t *testing.T) {
	records, log, backups := buildTestRecords()
	enforced, workspace, _ := buildRecordedSessionBroker(t, "default", records)
	writeWorkspaceFile(t, workspace, ".env", "TOKEN=secret")

	if _, err := enforced.WriteFile(core.WithToolCallID(testTurn, "call-9"), ".env", []byte("x")); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("WriteFile(.env) = %v, want ErrDenied", err)
	}
	if _, err := enforced.OpenFile(testTurn, ".env"); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("OpenFile(.env) = %v, want ErrDenied", err)
	}
	if len(backups.seen) != 0 {
		t.Fatalf("backed up %v for refused writes", backups.seen)
	}
	requireEntryKinds(t, log, core.EntryPolicyDenied, core.EntryPolicyDenied)
	denied := log.entries[0]
	if denied.Author != core.AuthorBroker || denied.Call != "call-9" || denied.Capability.Kind != core.CapabilityFSWrite ||
		!strings.HasSuffix(denied.Capability.Scope, ".env") || denied.Detail == "" {
		t.Fatalf("policy_denied = %+v %+v, want call-9's fs_write on .env with its rule", denied, denied.Capability)
	}
}

// §6.4: a write the broker cannot back up asks every time, once; it can never become a rule.
func TestIrreversibleWriteAsksOnceAndNeverAlways(t *testing.T) {
	records, log, backups := buildTestRecords()
	backups.maxBytes = 4
	enforced, workspace, _ := buildRecordedSessionBroker(t, "default", records)
	writeWorkspaceFile(t, workspace, "big.txt", "more than four bytes")

	var required *core.ApprovalRequiredError
	if _, err := enforced.WriteFile(testTurn, "big.txt", []byte("x")); !errors.As(err, &required) || !required.Operation.IsIrreversible {
		t.Fatalf("WriteFile(big) = %v, want an irreversible approval request", err)
	}
	if got := readWorkspaceFile(t, workspace, "big.txt"); got != "more than four bytes" {
		t.Fatalf("big.txt changed before anyone approved: %q", got)
	}
	asked := core.Capability{Kind: required.Operation.Kind, Scope: required.Operation.Target}
	rule := core.DecisionRule{Kind: asked.Kind, Scope: asked.Scope, Decision: core.DecisionAllow, Source: "test"}
	if err := enforced.AddDecisionRule(testTurn, rule); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("AddDecisionRule(irreversible) = %v, want ErrDenied", err)
	}

	if err := enforced.ApproveOnce(testTurn, asked); err != nil {
		t.Fatalf("ApproveOnce = %v", err)
	}
	if _, err := enforced.WriteFile(testTurn, "big.txt", []byte("x")); err != nil {
		t.Fatalf("WriteFile after ApproveOnce = %v", err)
	}
	if got := readWorkspaceFile(t, workspace, "big.txt"); got != "x" {
		t.Fatalf("big.txt = %q, want the approved write", got)
	}
	requireEntryKinds(t, log, core.EntryApprovalAsked, core.EntryApprovalGiven, core.EntryFileChanged)
	if change := log.entries[2].FileChange; !change.IsIrreversible || change.Before != "" {
		t.Fatalf("file_changed = %+v, want it marked irreversible with no backup", change)
	}

	writeWorkspaceFile(t, workspace, "big.txt", "more than four bytes")
	if _, err := enforced.WriteFile(testTurn, "big.txt", []byte("y")); !errors.As(err, &required) {
		t.Fatalf("second WriteFile(big) = %v, want it to ask again: once is spent", err)
	}
}

// Approving a policy Ask answers that question only; the write still asks about irreversibility.
func TestApprovingAPolicyAskDoesNotWaiveIrreversibility(t *testing.T) {
	records, _, backups := buildTestRecords()
	backups.maxBytes = 4
	enforced, workspace, _ := buildRecordedSessionBroker(t, "default", records)
	writeWorkspaceFile(t, workspace, "big.txt", "more than four bytes")
	untrusted := core.MarkUntrustedStep(testTurn)

	var required *core.ApprovalRequiredError
	if _, err := enforced.WriteFile(untrusted, "big.txt", []byte("x")); !errors.As(err, &required) || required.Operation.IsIrreversible {
		t.Fatalf("untrusted WriteFile = %v, want policy's own Ask first", err)
	}
	if err := enforced.ApproveOnce(untrusted, core.Capability{Kind: required.Operation.Kind, Scope: required.Operation.Target}); err != nil {
		t.Fatal(err)
	}
	for {
		_, err := enforced.WriteFile(untrusted, "big.txt", []byte("x"))
		if !errors.As(err, &required) {
			t.Fatalf("WriteFile = %v, want an irreversible approval request before writing", err)
		}
		if required.Operation.IsIrreversible {
			break
		}
		if err := enforced.ApproveOnce(untrusted, core.Capability{Kind: required.Operation.Kind, Scope: required.Operation.Target}); err != nil {
			t.Fatal(err)
		}
	}
	if got := readWorkspaceFile(t, workspace, "big.txt"); got != "more than four bytes" {
		t.Fatalf("big.txt = %q, want it untouched until the irreversibility is approved", got)
	}
}

// Travis reports what was said; the broker records it as travis's and refuses anything else.
func TestAppendTravisEntriesStampsThemAsTravisAndRefusesBrokerFacts(t *testing.T) {
	records, log, _ := buildTestRecords()
	enforced, _, _ := buildRecordedSessionBroker(t, "default", records)
	message := core.Message{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: "rename it"}}}
	claimed := core.SessionEntry{ID: "chosen-by-travis", Author: core.AuthorBroker, Kind: core.EntryMessage, Turn: "turn-1", Message: &message}

	if err := enforced.AppendTravisEntries(context.Background(), []core.SessionEntry{claimed}); err != nil {
		t.Fatal(err)
	}
	if entry := log.entries[0]; entry.Author != core.AuthorTravis || entry.ID == "chosen-by-travis" || entry.At.IsZero() {
		t.Fatalf("logged %+v, want the broker's own author, id and time", entry)
	}

	forged := core.SessionEntry{Kind: core.EntryFileChanged, Turn: "turn-1", FileChange: &core.FileChange{Path: filepath.Join(t.TempDir(), "a"), After: "ab"}}
	if err := enforced.AppendTravisEntries(context.Background(), []core.SessionEntry{forged}); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("AppendTravisEntries(file_changed) = %v, want ErrDenied", err)
	}
	if len(log.entries) != 1 {
		t.Fatalf("session log = %v, want the forged entry refused", log.reportKinds())
	}
}

func TestListTurnsSummarisesTheSessionLog(t *testing.T) {
	records, _, _ := buildTestRecords()
	enforced, _, _ := buildRecordedSessionBroker(t, "default", records)
	message := core.Message{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: "write notes"}}}
	if err := enforced.AppendTravisEntries(testTurn, []core.SessionEntry{{Kind: core.EntryMessage, Turn: "turn-1", Message: &message}}); err != nil {
		t.Fatal(err)
	}
	info, err := enforced.WriteFile(testTurn, "notes.md", []byte("hi"))
	if err != nil {
		t.Fatal(err)
	}

	turns, err := enforced.ListTurns(context.Background())
	if err != nil || len(turns) != 1 || turns[0].Turn != "turn-1" || turns[0].Prompt != "write notes" ||
		len(turns[0].ChangedPaths) != 1 || turns[0].ChangedPaths[0] != info.Path {
		t.Fatalf("ListTurns = %+v, %v", turns, err)
	}
}

// The remote side carries the turn and call across the wire, reports messages, and lists turns.
func TestRemoteBrokerRecordsMessagesAndWritesUnderTheirTurn(t *testing.T) {
	records, log, _ := buildTestRecords()
	enforced, _, _ := buildRecordedSessionBroker(t, "default", records)
	remote, _ := startPipedBroker(t, enforced)
	message := core.Message{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: "write notes"}}}

	if err := remote.AppendEntries(testTurn, []core.SessionEntry{{Kind: core.EntryMessage, Turn: "turn-1", Message: &message}}); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.WriteFile(core.WithToolCallID(testTurn, "call-2"), "notes.md", []byte("hi")); err != nil {
		t.Fatal(err)
	}
	requireEntryKinds(t, log, core.EntryMessage, core.EntryFileChanged)
	if change := log.entries[1]; change.Turn != "turn-1" || change.Call != "call-2" {
		t.Fatalf("file_changed filed under %q/%q, want turn-1/call-2", change.Turn, change.Call)
	}
	if text := log.entries[0].Message.Parts[0].(core.TextPart).Text; text != "write notes" {
		t.Fatalf("logged message = %q", text)
	}

	turns, err := remote.ListTurns(context.Background())
	if err != nil || len(turns) != 1 || turns[0].Prompt != "write notes" || len(turns[0].ChangedPaths) != 1 {
		t.Fatalf("ListTurns = %+v, %v", turns, err)
	}
}

func TestRemoteBrokerRefusesSessionLogOpsOnABrokerWithoutALog(t *testing.T) {
	remote, _ := startPipedBroker(t, broker.BuildLocalBroker(t.TempDir(), core.SystemClock{}))
	if _, err := remote.ListTurns(context.Background()); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("ListTurns = %v, want ErrInvalid", err)
	}
	if _, err := remote.DescribePath(context.Background(), "."); err != nil {
		t.Fatalf("a refused op broke the connection: %v", err)
	}
}

// approveIrreversibleOnce answers every Ask with once, noting whether it was told the write
// cannot be undone.
type approveIrreversibleOnce struct{ sawIrreversible bool }

func (a *approveIrreversibleOnce) RequestApproval(_ context.Context, op core.Operation) (core.ApprovalDecision, error) {
	a.sawIrreversible = a.sawIrreversible || op.IsIrreversible
	return core.ApprovalOnce, nil
}

func TestRemoteBrokerPutsAnIrreversibleWriteToTheHuman(t *testing.T) {
	records, _, backups := buildTestRecords()
	backups.maxBytes = 4
	enforced, workspace, _ := buildRecordedSessionBroker(t, "default", records)
	writeWorkspaceFile(t, workspace, "big.txt", "more than four bytes")
	approver := &approveIrreversibleOnce{}
	remote, _ := startPipedBrokerWith(t, enforced, broker.RemoteOptions{Approver: approver})

	if _, err := remote.WriteFile(testTurn, "big.txt", []byte("x")); err != nil {
		t.Fatalf("WriteFile = %v", err)
	}
	if !approver.sawIrreversible {
		t.Fatal("the human was not told the write cannot be undone")
	}
	if got := readWorkspaceFile(t, workspace, "big.txt"); got != "x" {
		t.Fatalf("big.txt = %q", got)
	}
}

const crashHelperEnv = "TRAVIS_BROKER_CRASH_HELPER"

// stallAfterLoggedChange is a session log that, once a file_changed entry is durable, reports
// ready and stalls, so the parent can kill -9 the process between the log and the write.
type stallAfterLoggedChange struct{ *filestore.Store }

func (l stallAfterLoggedChange) Append(entries ...core.SessionEntry) error {
	if err := l.Store.Append(entries...); err != nil {
		return err
	}
	if entries[0].Kind == core.EntryFileChanged {
		fmt.Println("ready")
		time.Sleep(time.Hour)
	}
	return nil
}

// TestCrashHelper is the broker TestCrashBetweenLoggingAndWritingLeavesAnUndoableFile kills.
func TestCrashHelper(t *testing.T) {
	if os.Getenv(crashHelperEnv) == "" {
		t.Skip("runs only as a crash-test child process")
	}
	workspace, stateDir := os.Getenv("WORKSPACE"), os.Getenv("STATE_DIR")
	sessionLog, err := filestore.Open(filepath.Join(stateDir, "sessions"), "crash")
	if err != nil {
		t.Fatal(err)
	}
	backups, err := blobs.Open(filepath.Join(stateDir, "blobs"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	paths := config.PathSet{WorkspaceDir: workspace, StateDir: stateDir}
	sessionPolicy, err := broker.BuildSessionPolicy("default", broker.SessionRoots{Paths: paths, Home: filepath.Dir(workspace), Temp: os.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	records := broker.SessionRecords{Log: stallAfterLoggedChange{sessionLog}, Backups: backups, IDs: &core.SequentialIDGenerator{Prefix: "e"}}
	enforced := broker.BuildPolicyEnforcedBroker(workspace, sessionPolicy, records, core.SystemClock{})
	if _, err := enforced.WriteFile(testTurn, os.Getenv("TARGET"), []byte("written by the agent")); err != nil {
		t.Fatal(err)
	}
	t.Fatal("the write finished; the crash helper should have stalled before it")
}

// A kill -9 after the change is logged but before the file is written leaves the file as it was,
// and a log entry whose backup restores it: undo after a crash is never worse than no undo.
func TestCrashBetweenLoggingAndWritingLeavesAnUndoableFile(t *testing.T) {
	for _, tc := range []struct{ name, original string }{{"existing file", "original bytes"}, {"new file", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			workspace, stateDir := filepath.Join(base, "workspace"), filepath.Join(base, "state")
			if err := os.MkdirAll(workspace, 0o750); err != nil {
				t.Fatal(err)
			}
			if tc.original != "" {
				writeWorkspaceFile(t, workspace, "a.txt", tc.original)
			}
			runCrashHelperUntilStalled(t, workspace, stateDir, "a.txt")

			target := filepath.Join(workspace, "a.txt")
			current, err := os.ReadFile(target)
			if tc.original == "" && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a.txt exists after the crash: %q, %v", current, err)
			}
			if tc.original != "" && string(current) != tc.original {
				t.Fatalf("a.txt = %q after the crash, want it untouched", current)
			}
			sessionLog, err := filestore.Open(filepath.Join(stateDir, "sessions"), "crash")
			if err != nil {
				t.Fatal(err)
			}
			defer sessionLog.Close()
			entries := sessionLog.ReadEntries()
			if len(entries) != 1 || entries[0].Kind != core.EntryFileChanged || entries[0].FileChange.Path != target {
				t.Fatalf("session log after the crash = %+v, want the one logged change", entries)
			}

			writeWorkspaceFile(t, workspace, "a.txt", "changed later")
			backups, err := blobs.Open(filepath.Join(stateDir, "blobs"), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			if err := backups.Restore(context.Background(), target, blobs.Ref(entries[0].FileChange.Before)); err != nil {
				t.Fatalf("restore from the logged backup: %v", err)
			}
			restored, err := os.ReadFile(target)
			if tc.original == "" && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("undoing the creation left a.txt: %q, %v", restored, err)
			}
			if tc.original != "" && string(restored) != tc.original {
				t.Fatalf("restored a.txt = %q, want %q", restored, tc.original)
			}
		})
	}
}

func runCrashHelperUntilStalled(t *testing.T, workspace, stateDir, target string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), crashHelperEnv+"=1", "WORKSPACE="+workspace, "STATE_DIR="+stateDir, "TARGET="+target)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait releases the child's file handles, which Windows needs before TempDir cleanup.
	defer func() { _ = cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if scanner.Text() == "ready" {
			return
		}
	}
	t.Fatalf("crash helper exited before stalling: %v", scanner.Err())
}
