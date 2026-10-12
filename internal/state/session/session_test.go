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
