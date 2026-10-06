// Package trace records a session's event timeline as JSONL, one file per session under
// the state dir (DESIGN.md §5.13). It only observes: a failed write is reported after the
// fact and never affects a turn. Traces are not redacted yet (§5.12.3).
package trace

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Kzeezee/travis/internal/core"
)

// Traces hold prompts and file contents, so only the user may read them.
const (
	traceDirMode  = 0o700
	traceFileMode = 0o600
)

// record is one JSONL line; the type tag lets a reader decode the event without guessing.
type record struct {
	Type  core.EventType `json:"type"`
	Event core.Event     `json:"event,omitempty"`
	// EncodeError replaces Event when the event cannot be marshalled, e.g. a tool call whose
	// model-written arguments are not valid JSON, so one bad event never ends the trace.
	EncodeError string `json:"encode_error,omitempty"`
}

// CreateSessionFile creates <dir>/<sessionID>.jsonl, refusing to overwrite an existing trace.
func CreateSessionFile(dir string, sessionID core.ID) (*os.File, error) {
	if err := os.MkdirAll(dir, traceDirMode); err != nil {
		return nil, fmt.Errorf("trace: create dir: %w", err)
	}
	path := filepath.Join(dir, string(sessionID)+".jsonl")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, traceFileMode)
	if err != nil {
		return nil, fmt.Errorf("trace: create file: %w", err)
	}
	return file, nil
}

// RecordEvents writes each event as one JSON line until events closes. Lines are buffered
// and flushed, then synced when the sink can sync, at every turn boundary and at the end,
// so a crash loses at most the turn in progress. After a write fails it keeps draining, so
// the bus does not log a drop for every later event, and returns that first error.
func RecordEvents(events <-chan core.Event, sink io.Writer) error {
	buffered := bufio.NewWriter(sink)
	var writeErr error
	for event := range events {
		if writeErr != nil {
			continue
		}
		writeErr = writeRecord(buffered, event)
		if _, isTurnEnd := event.(core.TurnCompleted); isTurnEnd && writeErr == nil {
			writeErr = flushToStorage(buffered, sink)
		}
	}
	if writeErr != nil {
		return writeErr
	}
	return flushToStorage(buffered, sink)
}

// writeRecord reports only write failures; an event that cannot be marshalled becomes a
// placeholder line instead.
func writeRecord(w io.Writer, event core.Event) error {
	var line bytes.Buffer
	encoder := json.NewEncoder(&line)
	// Traces are read by people, so keep <, > and & literal in code and markup.
	encoder.SetEscapeHTML(false)
	eventType := event.ReportEventType()
	if err := encoder.Encode(record{Type: eventType, Event: event}); err != nil {
		line.Reset()
		_ = encoder.Encode(record{Type: eventType, EncodeError: err.Error()})
	}
	if _, err := w.Write(line.Bytes()); err != nil {
		return fmt.Errorf("trace: write: %w", err)
	}
	return nil
}

func flushToStorage(buffered *bufio.Writer, sink io.Writer) error {
	if err := buffered.Flush(); err != nil {
		return fmt.Errorf("trace: write: %w", err)
	}
	if syncer, canSync := sink.(interface{ Sync() error }); canSync {
		if err := syncer.Sync(); err != nil {
			return fmt.Errorf("trace: sync: %w", err)
		}
	}
	return nil
}
