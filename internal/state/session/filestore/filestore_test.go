package filestore_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/session/filestore"
)

func openStore(t *testing.T, dir string) *filestore.Store {
	t.Helper()
	store, err := filestore.Open(dir, "s1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func buildEntries(t *testing.T) []core.SessionEntry {
	t.Helper()
	path, err := filepath.Abs("a.go")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC)
	message := core.Message{Role: core.RoleAssistant, Parts: []core.Part{
		core.TextPart{Text: "writing"},
		core.ToolCallPart{ToolCall: core.ToolCall{ID: "c1", Name: "fs_write", Arguments: []byte(`{"path":"a.go"}`)}},
	}}
	return []core.SessionEntry{
		{ID: "e1", At: at, Author: core.AuthorTravis, Kind: core.EntryMessage, Turn: "t1", Message: &message},
		{ID: "e2", At: at, Author: core.AuthorBroker, Kind: core.EntryFileChanged, Turn: "t1", Call: "c1",
			FileChange: &core.FileChange{Path: path, After: "ab"}},
	}
}

func TestAppendedEntriesReplayAfterReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := filestore.Open(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(buildEntries(t)...); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if got := openStore(t, dir).ReadEntries(); !reflect.DeepEqual(got, buildEntries(t)) {
		t.Fatalf("replayed = %+v\nwant      %+v", got, buildEntries(t))
	}
}

func TestAppendRefusesAnInvalidEntryAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	invalid := buildEntries(t)
	invalid[1].FileChange.Path = "relative.go"
	if err := store.Append(invalid...); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Append = %v, want ErrInvalid", err)
	}
	if got := store.ReadEntries(); len(got) != 0 {
		t.Fatalf("entries after a refused append = %v", got)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "s1.jsonl")); len(data) != 0 {
		t.Fatalf("log after a refused append = %q", data)
	}
}

func TestTornLastLineIsDroppedOnReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := filestore.Open(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(buildEntries(t)[0]); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	appendRaw(t, filepath.Join(dir, "s1.jsonl"), `{"id":"e2","kind":"file_ch`)

	reopened := openStore(t, dir)
	if got := reopened.ReadEntries(); len(got) != 1 {
		t.Fatalf("entries after a torn tail = %v, want the one complete entry", got)
	}
	if err := reopened.Append(buildEntries(t)[1]); err != nil {
		t.Fatal(err)
	}
	if got := openStore(t, dir).ReadEntries(); !reflect.DeepEqual(got, buildEntries(t)) {
		t.Fatalf("an append after the torn tail glued onto it: %+v", got)
	}
}

func TestCorruptLineInTheMiddleFailsToOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte("{not json}\n{\"also\":\"bad\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := filestore.Open(dir, "s1"); err == nil {
		t.Fatal("Open accepted a log damaged before its last line")
	}
}

func TestCorruptCompleteLastLineFailsToOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte("{not json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := filestore.Open(dir, "s1"); err == nil {
		t.Fatal("Open discarded a complete but corrupt last line")
	}
}

func TestOpenRefusesSessionIDsThatAreNotPlainNames(t *testing.T) {
	for _, id := range []core.ID{"", "../escape", "a/b"} {
		if _, err := filestore.Open(t.TempDir(), id); err == nil {
			t.Errorf("Open accepted session id %q", id)
		}
	}
}

func TestLogIsPrivateToTheUser(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("POSIX permission bits")
	}
	dir := filepath.Join(t.TempDir(), "sessions")
	store := openStore(t, dir)
	if err := store.Append(buildEntries(t)...); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "s1.jsonl"): 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s mode = %v, %v, want %v", path, info.Mode().Perm(), err, want)
		}
	}
}

func appendRaw(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
