package trace_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/eval/trace"
)

var errDiskFull = errors.New("disk full")

// recordingSink is a syncable writer whose contents can be read while recording runs.
type recordingSink struct {
	mu        sync.Mutex
	written   bytes.Buffer
	syncCount int
}

func (s *recordingSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written.Write(p)
}

func (s *recordingSink) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncCount++
	return nil
}

func (s *recordingSink) reportState() (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written.String(), s.syncCount
}

type failingSink struct{}

func (failingSink) Write([]byte) (int, error) { return 0, errDiskFull }

type decodedRecord struct {
	Type        core.EventType  `json:"type"`
	Event       json.RawMessage `json:"event"`
	EncodeError string          `json:"encode_error"`
}

func buildHeader(id, turn string) core.EventHeader {
	return core.EventHeader{ID: core.ID(id), TurnID: core.ID(turn), At: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}
}

func decodeRecords(t *testing.T, written string) []decodedRecord {
	t.Helper()
	var records []decodedRecord
	scanner := bufio.NewScanner(strings.NewReader(written))
	for scanner.Scan() {
		var record decodedRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("line is not JSON: %q (%v)", scanner.Text(), err)
		}
		records = append(records, record)
	}
	return records
}

func recordAll(t *testing.T, sink *recordingSink, events ...core.Event) {
	t.Helper()
	channel := make(chan core.Event, len(events))
	for _, event := range events {
		channel <- event
	}
	close(channel)
	if err := trace.RecordEvents(channel, sink); err != nil {
		t.Fatalf("RecordEvents returned %v, want nil", err)
	}
}

func TestRecordEventsWritesOneTaggedLinePerEventInOrder(t *testing.T) {
	sink := &recordingSink{}
	recordAll(t, sink,
		core.ToolCallStarted{EventHeader: buildHeader("e1", "t1"), Call: core.ToolCall{ID: "c1", Name: "grep", Arguments: json.RawMessage(`{"pattern":"<TODO>"}`)}},
		core.ToolCallFinished{EventHeader: buildHeader("e2", "t1"), Result: core.ToolResult{CallID: "c1", Name: "grep", Content: "a.md:1:x"}},
		core.TurnCompleted{EventHeader: buildHeader("e3", "t1"), Reason: core.StopEndTurn},
	)

	written, _ := sink.reportState()
	records := decodeRecords(t, written)
	wantTypes := []core.EventType{core.EventToolCallStarted, core.EventToolCallFinished, core.EventTurnCompleted}
	if len(records) != len(wantTypes) {
		t.Fatalf("got %d records, want %d:\n%s", len(records), len(wantTypes), written)
	}
	for i, record := range records {
		if record.Type != wantTypes[i] {
			t.Fatalf("record %d type = %q, want %q", i, record.Type, wantTypes[i])
		}
	}
	var started core.ToolCallStarted
	if err := json.Unmarshal(records[0].Event, &started); err != nil {
		t.Fatal(err)
	}
	if started.TurnID != "t1" || started.Call.ID != "c1" {
		t.Fatalf("decoded %+v, want turn t1 and call c1", started)
	}
	if !strings.Contains(written, `"pattern":"<TODO>"`) {
		t.Fatalf("markup was escaped, want it literal:\n%s", written)
	}
}

func TestRecordEventsFlushesAndSyncsAtEachTurnBoundary(t *testing.T) {
	sink := &recordingSink{}
	channel := make(chan core.Event)
	done := make(chan error, 1)
	go func() { done <- trace.RecordEvents(channel, sink) }()

	channel <- core.AssistantTextStreamed{EventHeader: buildHeader("e1", "t1"), Text: "hi"}
	channel <- core.TurnCompleted{EventHeader: buildHeader("e2", "t1"), Reason: core.StopEndTurn}
	// An unbuffered send returns only once the recorder took it, so the turn end is handled.
	channel <- core.AssistantTextStreamed{EventHeader: buildHeader("e3", "t2"), Text: "next"}

	written, syncs := sink.reportState()
	if records := decodeRecords(t, written); len(records) != 2 || syncs != 1 {
		t.Fatalf("mid-session: got %d records and %d syncs, want the finished turn on disk", len(records), syncs)
	}

	close(channel)
	if err := <-done; err != nil {
		t.Fatalf("RecordEvents returned %v, want nil", err)
	}
	written, syncs = sink.reportState()
	if records := decodeRecords(t, written); len(records) != 3 || syncs != 2 {
		t.Fatalf("at close: got %d records and %d syncs, want everything flushed", len(records), syncs)
	}
}

func TestRecordEventsKeepsGoingPastAnEventThatCannotBeEncoded(t *testing.T) {
	sink := &recordingSink{}
	recordAll(t, sink,
		core.ToolCallStarted{EventHeader: buildHeader("e1", "t1"), Call: core.ToolCall{ID: "c1", Name: "grep", Arguments: json.RawMessage(`{"pattern":`)}},
		core.TurnCompleted{EventHeader: buildHeader("e2", "t1"), Reason: core.StopEndTurn},
	)

	written, _ := sink.reportState()
	records := decodeRecords(t, written)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2:\n%s", len(records), written)
	}
	if records[0].Type != core.EventToolCallStarted || records[0].EncodeError == "" || len(records[0].Event) != 0 {
		t.Fatalf("got %+v, want a placeholder carrying the encode error", records[0])
	}
	if records[1].Type != core.EventTurnCompleted {
		t.Fatalf("got %q after the placeholder, want the turn to continue", records[1].Type)
	}
}

func TestRecordEventsDrainsAfterAWriteFailureAndReportsIt(t *testing.T) {
	channel := make(chan core.Event)
	done := make(chan error, 1)
	go func() { done <- trace.RecordEvents(channel, failingSink{}) }()

	for range 3 {
		channel <- core.TurnCompleted{EventHeader: buildHeader("e", "t"), Reason: core.StopEndTurn}
	}
	close(channel)

	if err := <-done; !errors.Is(err, errDiskFull) {
		t.Fatalf("RecordEvents returned %v, want the write failure", err)
	}
}

func TestCreateSessionFileNamesTheTraceAfterTheSessionAndNeverOverwrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "traces")

	file, err := trace.CreateSessionFile(dir, "01SESSION")
	if err != nil {
		t.Fatalf("CreateSessionFile returned %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "01SESSION.jsonl")); err != nil {
		t.Fatalf("trace file missing: %v", err)
	}
	if _, err := trace.CreateSessionFile(dir, "01SESSION"); err == nil {
		t.Fatal("expected an error when the session trace already exists")
	}
}
