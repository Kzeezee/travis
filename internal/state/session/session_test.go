package session_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/session"
)

var at = time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC)

func buildMessageEntry(turn core.ID, role core.Role, text string) core.SessionEntry {
	message := core.Message{Role: role, Parts: []core.Part{core.TextPart{Text: text}}}
	return core.SessionEntry{ID: core.ID("e-" + text), At: at, Author: core.AuthorTravis, Kind: core.EntryMessage, Turn: turn, Message: &message}
}

func buildFileChangedEntry(turn core.ID, path string, isIrreversible bool) core.SessionEntry {
	return core.SessionEntry{
		ID: core.ID("e-" + path), At: at, Author: core.AuthorBroker, Kind: core.EntryFileChanged, Turn: turn, Call: "c1",
		FileChange: &core.FileChange{Path: path, After: "ab", IsIrreversible: isIrreversible},
	}
}

func buildAbsolutePath(name string) string {
	return filepath.Join(string(filepath.Separator), "w", name)
}

func TestBuildHistoryReplaysMessagesInLogOrder(t *testing.T) {
	entries := []core.SessionEntry{
		buildMessageEntry("t1", core.RoleUser, "hi"),
		buildFileChangedEntry("t1", buildAbsolutePath("a.go"), false),
		buildMessageEntry("t1", core.RoleAssistant, "hello"),
		buildMessageEntry("t2", core.RoleUser, "again"),
	}
	var texts []string
	for _, message := range session.BuildHistory(entries) {
		texts = append(texts, message.Parts[0].(core.TextPart).Text)
	}
	if want := []string{"hi", "hello", "again"}; !slices.Equal(texts, want) {
		t.Fatalf("history = %v, want %v", texts, want)
	}
}

func TestBuildTurnSummariesKeepsStartOrderPromptAndChangedPaths(t *testing.T) {
	a, b := buildAbsolutePath("a.go"), buildAbsolutePath("b.go")
	entries := []core.SessionEntry{
		buildMessageEntry("t1", core.RoleUser, "rename the loader"),
		buildMessageEntry("t1", core.RoleAssistant, "on it"),
		buildFileChangedEntry("t1", a, false),
		buildFileChangedEntry("t1", a, false),
		buildFileChangedEntry("t1", b, true),
		buildMessageEntry("t2", core.RoleUser, "why?"),
		{ID: "d", At: at, Author: core.AuthorBroker, Kind: core.EntryPolicyDenied, Capability: &core.Capability{Kind: core.CapabilityFSRead}},
	}
	want := []core.TurnSummary{
		{Turn: "t1", Prompt: "rename the loader", At: at, ChangedPaths: []string{a, b}, HasIrreversible: true},
		{Turn: "t2", Prompt: "why?", At: at},
	}
	if got := session.BuildTurnSummaries(entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("summaries = %+v\nwant       %+v", got, want)
	}
}

func TestValidateEntryRejectsMalformedEntries(t *testing.T) {
	valid := buildMessageEntry("t1", core.RoleUser, "hi")
	system := core.Message{Role: core.RoleSystem}
	for name, entry := range map[string]core.SessionEntry{
		"unknown author":        func() core.SessionEntry { e := valid; e.Author = "model"; return e }(),
		"unknown kind":          func() core.SessionEntry { e := valid; e.Kind = "rewritten"; return e }(),
		"message without turn":  func() core.SessionEntry { e := valid; e.Turn = ""; return e }(),
		"message without body":  func() core.SessionEntry { e := valid; e.Message = nil; return e }(),
		"system message":        func() core.SessionEntry { e := valid; e.Message = &system; return e }(),
		"message with a change": func() core.SessionEntry { e := valid; e.FileChange = &core.FileChange{}; return e }(),
		"relative change path":  buildFileChangedEntry("t1", "a.go", false),
		"change without turn":   buildFileChangedEntry("", buildAbsolutePath("a.go"), false),
		"denial without capability": {
			Author: core.AuthorBroker, Kind: core.EntryPolicyDenied,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := session.ValidateEntry(entry); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("ValidateEntry = %v, want ErrInvalid", err)
			}
		})
	}
	if err := session.ValidateEntry(valid); err != nil {
		t.Fatalf("ValidateEntry(valid) = %v", err)
	}
}

// Travis reports what was said; only the broker can record what it did (§4.4).
func TestValidateTravisReportedRefusesBrokerOnlyKinds(t *testing.T) {
	for _, entry := range []core.SessionEntry{
		buildFileChangedEntry("t1", buildAbsolutePath("a.go"), false),
		{Kind: core.EntryPolicyDenied, Capability: &core.Capability{Kind: core.CapabilityFSRead}},
		{Kind: core.EntryApprovalGiven, Capability: &core.Capability{Kind: core.CapabilityFSWrite}},
	} {
		if err := session.ValidateTravisReported(entry); !errors.Is(err, core.ErrDenied) {
			t.Errorf("ValidateTravisReported(%s) = %v, want ErrDenied", entry.Kind, err)
		}
	}
	claimed := buildMessageEntry("t1", core.RoleUser, "hi")
	claimed.Author = core.AuthorBroker
	if err := session.ValidateTravisReported(claimed); err != nil {
		t.Fatalf("ValidateTravisReported(message) = %v; the author it claims is overwritten, not trusted", err)
	}
}

func buildChange(id core.ID, turn core.ID, path, before, after string) core.SessionEntry {
	return core.SessionEntry{
		ID: id, At: at, Author: core.AuthorBroker, Kind: core.EntryFileChanged, Turn: turn, Call: "c",
		FileChange: &core.FileChange{Path: path, Before: before, After: after},
	}
}

func buildRewindEntry(kind core.SessionEntryKind, fromTurn core.ID, mode core.RewindMode, changeIDs ...core.ID) core.SessionEntry {
	return core.SessionEntry{ID: core.ID("r-" + string(fromTurn)), At: at, Author: core.AuthorBroker, Kind: kind,
		Rewind: &core.RewindRecord{FromTurn: fromTurn, Mode: mode, ChangeIDs: changeIDs}}
}

// Three turns each edit x: reverting from the second plans x back to what the second found, and
// expects what the third wrote.
func TestPlanRevertTakesTheEarliestBeforeAndTheLatestAfterPerFile(t *testing.T) {
	x, y := buildAbsolutePath("x"), buildAbsolutePath("y")
	entries := []core.SessionEntry{
		buildMessageEntry("t1", core.RoleUser, "one"), buildChange("c1", "t1", x, "x0", "x1"),
		buildMessageEntry("t2", core.RoleUser, "two"), buildChange("c2", "t2", x, "x1", "x2"), buildChange("c3", "t2", y, "", "y1"),
		buildMessageEntry("t3", core.RoleUser, "three"), buildChange("c4", "t3", x, "x2", "x3"),
	}
	plans, err := session.PlanRevert(entries, "t2")
	if err != nil {
		t.Fatal(err)
	}
	want := []session.FileRevertPlan{
		{Path: x, Before: "x1", ExpectedAfter: "x3", ChangeIDs: []core.ID{"c2", "c4"}},
		{Path: y, Before: "", ExpectedAfter: "y1", ChangeIDs: []core.ID{"c3"}},
	}
	if !reflect.DeepEqual(plans, want) {
		t.Fatalf("plans = %+v\nwant    %+v", plans, want)
	}
}

func TestPlanRevertSkipsChangesAnEarlierRewindReverted(t *testing.T) {
	x := buildAbsolutePath("x")
	entries := []core.SessionEntry{
		buildMessageEntry("t1", core.RoleUser, "one"), buildChange("c1", "t1", x, "x0", "x1"),
		buildMessageEntry("t2", core.RoleUser, "two"), buildChange("c2", "t2", x, "x1", "x2"),
		buildRewindEntry(core.EntryFilesReverted, "t2", core.RewindFilesOnly, "c2"),
	}
	plans, err := session.PlanRevert(entries, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Before != "x0" || plans[0].ExpectedAfter != "x1" {
		t.Fatalf("plans = %+v, want x back to x0, expecting t1's x1 now that t2's change is reverted", plans)
	}
}

// A conversation-only rewind hides turns from the chat but leaves their files changed; reverting
// files from an earlier turn still covers them, because log order decides.
func TestPlanRevertCoversTurnsTheConversationNoLongerShows(t *testing.T) {
	x := buildAbsolutePath("x")
	entries := []core.SessionEntry{
		buildMessageEntry("t1", core.RoleUser, "one"),
		buildMessageEntry("t2", core.RoleUser, "two"), buildChange("c2", "t2", x, "", "x2"),
		buildRewindEntry(core.EntryRewound, "t2", core.RewindConversationOnly),
	}
	plans, err := session.PlanRevert(entries, "t1")
	if err != nil || len(plans) != 1 || plans[0].Path != x {
		t.Fatalf("plans = %+v, %v; want t2's hidden change planned", plans, err)
	}
}

func TestPlanRevertMarksAFileWhoseEarliestChangeHasNoBackup(t *testing.T) {
	x := buildAbsolutePath("x")
	irreversible := buildChange("c1", "t1", x, "", "x1")
	irreversible.FileChange.IsIrreversible = true
	entries := []core.SessionEntry{buildMessageEntry("t1", core.RoleUser, "one"), irreversible, buildChange("c2", "t1", x, "x1", "x2")}
	plans, _ := session.PlanRevert(entries, "t1")
	if len(plans) != 1 || !plans[0].HasNoBackup {
		t.Fatalf("plans = %+v, want x marked as having no backup", plans)
	}
	if later, _ := session.PlanRevert(append(entries, buildMessageEntry("t2", core.RoleUser, "two"), buildChange("c3", "t2", x, "x2", "x3")), "t2"); later[0].HasNoBackup {
		t.Fatal("a later change with a backup is restorable even though an earlier one was not")
	}
}

func TestPlanRevertRefusesATurnNotInTheSession(t *testing.T) {
	if _, err := session.PlanRevert([]core.SessionEntry{buildMessageEntry("t1", core.RoleUser, "one")}, "t9"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("PlanRevert = %v, want ErrInvalid", err)
	}
}

func TestBuildTurnSummariesHideRewoundTurnsAndRevertedChanges(t *testing.T) {
	x, y := buildAbsolutePath("x"), buildAbsolutePath("y")
	entries := []core.SessionEntry{
		buildMessageEntry("t1", core.RoleUser, "one"), buildChange("c1", "t1", x, "", "x1"), buildChange("c2", "t1", y, "", "y1"),
		buildRewindEntry(core.EntryFilesReverted, "t1", core.RewindFilesOnly, "c2"),
		buildMessageEntry("t2", core.RoleUser, "two"),
		buildRewindEntry(core.EntryRewound, "t2", core.RewindConversationOnly),
	}
	summaries := session.BuildTurnSummaries(entries)
	if len(summaries) != 1 || summaries[0].Turn != "t1" || !slices.Equal(summaries[0].ChangedPaths, []string{x}) {
		t.Fatalf("summaries = %+v, want only t1, with only its unreverted change", summaries)
	}
}

func TestValidateEntryChecksRewindEntriesAgainstTheirMode(t *testing.T) {
	for name, entry := range map[string]core.SessionEntry{
		"no record":               {Author: core.AuthorBroker, Kind: core.EntryRewound},
		"unknown mode":            buildRewindEntry(core.EntryRewound, "t1", "everything"),
		"rewound in files mode":   buildRewindEntry(core.EntryRewound, "t1", core.RewindFilesOnly),
		"files in chat-only mode": buildRewindEntry(core.EntryFilesReverted, "t1", core.RewindConversationOnly),
		"no turn":                 buildRewindEntry(core.EntryRewound, "", core.RewindFilesAndConversation),
	} {
		t.Run(name, func(t *testing.T) {
			if err := session.ValidateEntry(entry); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("ValidateEntry = %v, want ErrInvalid", err)
			}
		})
	}
	if err := session.ValidateTravisReported(buildRewindEntry(core.EntryRewound, "t1", core.RewindConversationOnly)); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("travis reporting a rewind = %v, want ErrDenied: only the broker rewinds", err)
	}
}
