package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/providers/cassette"
	"github.com/Kzeezee/travis/internal/providers/openaicompat"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/security/redact"
	"github.com/Kzeezee/travis/internal/state/blobs"
	"github.com/Kzeezee/travis/internal/state/memory"
	"github.com/Kzeezee/travis/internal/state/session/filestore"
)

// notesTask is the TRA-17 exit task over testdata/fixtures/notes, written down so the 6A
// scenario harness can lift it as is.
var notesTask = struct {
	question   string
	answerNote string
	answerDate string
}{
	question:   "Which note mentions the dentist appointment, and what date is it on?",
	answerNote: "journal/2026-09-14-café.md",
	answerDate: "Tuesday 13 October 2026",
}

const e2eMaxSteps = 6

// scriptedStep plays the model for one provider call. It reads the request it was sent,
// so each step depends on the tool results the real tools returned.
type scriptedStep func(t *testing.T, request core.ProviderRequest) []core.Chunk

// scriptedProvider is test infrastructure, not the D-16 cassette player: it hand-plays a
// plausible model so the rest of the wiring can run for real without a network.
type scriptedProvider struct {
	t        *testing.T
	steps    []scriptedStep
	requests []core.ProviderRequest
}

func (p *scriptedProvider) Chat(_ context.Context, request core.ProviderRequest) (core.Stream, error) {
	p.requests = append(p.requests, request)
	step := len(p.requests) - 1
	if step >= len(p.steps) {
		return nil, fmt.Errorf("scripted provider: no step %d", step+1)
	}
	return &chunkStream{chunks: p.steps[step](p.t, request)}, nil
}

type chunkStream struct {
	chunks []core.Chunk
}

func (s *chunkStream) Next() (core.Chunk, error) {
	if len(s.chunks) == 0 {
		return core.Chunk{}, io.EOF
	}
	chunk := s.chunks[0]
	s.chunks = s.chunks[1:]
	return chunk, nil
}

func (s *chunkStream) Close() error { return nil }

func buildToolCallChunks(id core.ID, name, arguments string) []core.Chunk {
	call := core.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(arguments)}
	return []core.Chunk{{ToolCalls: []core.ToolCall{call}, Finish: core.StopToolCalls}}
}

// readLatestToolResult returns the result the loop sent back for the previous step's call.
func readLatestToolResult(t *testing.T, request core.ProviderRequest) core.ToolResult {
	t.Helper()
	last := request.Messages[len(request.Messages)-1]
	if last.Role != core.RoleTool || len(last.Parts) != 1 {
		t.Fatalf("last message is %q with %d parts, want one tool result", last.Role, len(last.Parts))
	}
	part, isResult := last.Parts[0].(core.ToolResultPart)
	if !isResult || part.ToolResult.IsError {
		t.Fatalf("last part is %#v, want a successful tool result", last.Parts[0])
	}
	return part.ToolResult
}

type fixedQuestioner struct {
	question string
}

func (q *fixedQuestioner) AskUser(_ context.Context, question string) (string, error) {
	q.question = question
	return "Friday", nil
}

func TestAskUserAndMemoryRunThroughTheRealConversationAndBroker(t *testing.T) {
	provider := &scriptedProvider{t: t, steps: []scriptedStep{
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			offered := map[string]bool{}
			for _, spec := range request.Tools {
				offered[spec.Name] = true
			}
			if !offered["ask_user"] || !offered["memory"] {
				t.Fatalf("missing core tools: %v", offered)
			}
			return buildToolCallChunks("ask", "ask_user", `{"question":"Which day?"}`)
		},
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			if got := readLatestToolResult(t, request).Content; got != "Friday" {
				t.Fatalf("answer = %q", got)
			}
			return buildToolCallChunks("save", "memory", `{"action":"replace","content":"Deadline: Friday"}`)
		},
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			requireContains(t, "memory replace", readLatestToolResult(t, request).Content, "Deadline: Friday")
			return buildToolCallChunks("read", "memory", `{"action":"read"}`)
		},
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			requireContains(t, "memory read", readLatestToolResult(t, request).Content, "Deadline: Friday")
			return []core.Chunk{{Text: "I remembered Friday.", Finish: core.StopEndTurn}}
		},
	}}
	paths := config.PathSet{WorkspaceDir: t.TempDir(), StateDir: t.TempDir(), RuntimeDir: buildShortRuntimeDir(t)}
	var diagnostics strings.Builder
	bus := buildSessionBus(&diagnostics)
	remote, stateDir := startTestSessionBroker(t, paths, &diagnostics, broker.RemoteOptions{Events: bus})
	paths.StateDir = stateDir
	questioner := &fixedQuestioner{}
	cfg := &config.Config{Brain: config.BrainCfg{MaxSteps: 5}, Tools: config.ToolsCfg{MaxExposed: 60}}
	session, err := assembleChatSession(chatSessionDeps{
		Cfg: cfg, Paths: paths, Session: testSessionID, Provider: provider, Model: "scripted-model",
		Files: remote, Log: remote, Memory: remote, Commands: remote, Pages: remote,
		Bus: bus, Diagnostics: &diagnostics, Redactor: redact.New(),
		Questioner: questioner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.conversation.RunAssistantTurn(context.Background(), "remember my deadline"); err != nil {
		t.Fatal(err)
	}
	session.close()
	if questioner.question != "Which day?" {
		t.Fatalf("question = %q", questioner.question)
	}
	stored, err := os.ReadFile(filepath.Join(stateDir, memory.FileName))
	if err != nil || !strings.Contains(string(stored), "Deadline: Friday") {
		t.Fatalf("memory file = %q, %v", stored, err)
	}
}

func requireContains(t *testing.T, step, content, want string) {
	t.Helper()
	if !strings.Contains(content, want) {
		t.Fatalf("%s: tool result lacks %q:\n%s", step, want, content)
	}
}

// buildNotesTaskScript searches contents for the note, reads it, lists the folder it points
// at, reads the confirmation there, and answers from what the tools returned.
func buildNotesTaskScript() []scriptedStep {
	var notePath string
	return []scriptedStep{
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			offered := map[string]bool{}
			for _, spec := range request.Tools {
				offered[spec.Name] = true
			}
			if !offered["grep"] || !offered["fs_read"] {
				t.Fatalf("offered tools %v, want grep and fs_read from the real registry", request.Tools)
			}
			return buildToolCallChunks("call-search", "grep", `{"pattern":"(?i)dentist"}`)
		},
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			match := readLatestToolResult(t, request).Content
			notePath, _, _ = strings.Cut(match, ":")
			if notePath != notesTask.answerNote || strings.Contains(match, "\n") {
				t.Fatalf("content search returned %q, want one match in %q", match, notesTask.answerNote)
			}
			arguments, err := json.Marshal(map[string]string{"path": notePath})
			if err != nil {
				t.Fatal(err)
			}
			return buildToolCallChunks("call-read-note", "fs_read", string(arguments))
		},
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			note := readLatestToolResult(t, request).Content
			requireContains(t, "fs_read note", note, "Finally booked the dentist")
			requireContains(t, "fs_read note", note, "filed under admin/")
			return buildToolCallChunks("call-list", "grep", `{"mode":"files","path":"admin"}`)
		},
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			listing := readLatestToolResult(t, request).Content
			if listing != "admin/clinic-confirmation.md\nadmin/insurance.md" {
				t.Fatalf("listing admin/ returned %q, want both files", listing)
			}
			return buildToolCallChunks("call-read-confirmation", "fs_read", `{"path":"admin/clinic-confirmation.md"}`)
		},
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			confirmation := readLatestToolResult(t, request).Content
			// fs_read returns file bytes as they are, so this fails if a checkout rewrote the CRLF fixture.
			const line = "3\tAppointment: Tuesday 13 October 2026, 09:30\r\n"
			requireContains(t, "fs_read confirmation", confirmation, line)
			var date string
			for _, numbered := range strings.Split(confirmation, "\n") {
				if appointment, isAppointment := strings.CutPrefix(numbered, "3\tAppointment: "); isAppointment {
					date, _, _ = strings.Cut(appointment, ",")
				}
			}
			answer := fmt.Sprintf("%s mentions the dentist appointment; it is on %s.", notePath, date)
			half := len(answer) / 2
			return []core.Chunk{{Text: answer[:half]}, {Text: answer[half:], Finish: core.StopEndTurn}}
		},
	}
}

type traceLine struct {
	Type  core.EventType `json:"type"`
	Event struct {
		TurnID core.ID `json:"turn_id"`
		Call   struct {
			ID core.ID `json:"id"`
		} `json:"call"`
		Result struct {
			CallID core.ID `json:"call_id"`
		} `json:"result"`
	} `json:"event"`
}

func readTraceLines(t *testing.T, stateDir string) []traceLine {
	t.Helper()
	traces, err := filepath.Glob(filepath.Join(stateDir, "traces", "*.jsonl"))
	if err != nil || len(traces) != 1 {
		t.Fatalf("got traces %v (%v), want one session trace", traces, err)
	}
	file, err := os.Open(traces[0])
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var lines []traceLine
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line traceLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("trace line is not JSON: %q", scanner.Text())
		}
		lines = append(lines, line)
	}
	return lines
}

// notesTaskRun is what one notes-task turn produced.
type notesTaskRun struct {
	calls         []core.ID
	answer        string
	finalThoughts string // reasoning streamed after the last tool call, alongside the answer
	stateDir      string
	diagnostics   string
}

// runNotesTask runs the notes task through the real wiring with the given model and checks
// what any model must do to pass: 3+ successful tool calls, each finished in order, then a
// final-text end_turn.
func runNotesTask(t *testing.T, provider core.Provider, model string) notesTaskRun {
	t.Helper()
	workspace, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "notes"))
	if err != nil {
		t.Fatal(err)
	}
	paths := config.PathSet{WorkspaceDir: workspace, StateDir: t.TempDir(), RuntimeDir: buildShortRuntimeDir(t)}
	cfg := &config.Config{
		Brain: config.BrainCfg{MaxSteps: e2eMaxSteps},
		Tools: config.ToolsCfg{MaxExposed: 60},
		Eval:  config.EvalCfg{Traces: true},
	}
	var diagnostics strings.Builder
	bus := buildSessionBus(&diagnostics)
	remote, _ := startTestSessionBroker(t, paths, &diagnostics, broker.RemoteOptions{Events: bus})
	session, err := assembleChatSession(chatSessionDeps{
		Cfg: cfg, Paths: paths, Session: testSessionID, Provider: provider, Model: model,
		Files: remote, Log: remote, Memory: remote, Commands: remote, Pages: remote,
		Bus: bus, Diagnostics: &diagnostics, Redactor: redact.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	events, _ := session.bus.Subscribe(nil)
	turnErr := session.conversation.RunAssistantTurn(context.Background(), notesTask.question)
	session.close()
	if turnErr != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", turnErr)
	}

	var started, finished []core.ID
	var answer, finalThoughts strings.Builder
	var completed *core.TurnCompleted
	for event := range events {
		switch fact := event.(type) {
		case core.ToolCallStarted:
			started = append(started, fact.Call.ID)
			finalThoughts.Reset()
		case core.ThoughtStreamed:
			finalThoughts.WriteString(fact.Text)
		case core.ToolCallFinished:
			if fact.Result.IsError {
				t.Fatalf("tool %s failed: %s", fact.Result.Name, fact.Result.Content)
			}
			finished = append(finished, fact.Result.CallID)
		case core.AssistantTextStreamed:
			answer.WriteString(fact.Text)
		case core.TurnCompleted:
			completed = &fact
		}
	}

	if len(started) < 3 || !slices.Equal(started, finished) {
		t.Fatalf("started %v, finished %v: want 3+ calls, each finished in order", started, finished)
	}
	if completed == nil || completed.Reason != core.StopEndTurn {
		t.Fatalf("turn completed with %+v, want a final-text end_turn", completed)
	}
	return notesTaskRun{calls: started, answer: answer.String(), finalThoughts: finalThoughts.String(), stateDir: paths.StateDir, diagnostics: diagnostics.String()}
}

// TestNotesTaskRunsEndToEndThroughTheRealWiring is the Phase 1 exit gate (TRA-17): the
// real loop, registry, grep and fs_read over the fixture, bus and trace, with only the
// model scripted.
func TestNotesTaskRunsEndToEndThroughTheRealWiring(t *testing.T) {
	provider := &scriptedProvider{t: t, steps: buildNotesTaskScript()}
	run := runNotesTask(t, provider, "scripted-model")

	if len(provider.requests) > e2eMaxSteps {
		t.Fatalf("took %d provider calls, want at most brain.max_steps=%d", len(provider.requests), e2eMaxSteps)
	}
	wantAnswer := fmt.Sprintf("%s mentions the dentist appointment; it is on %s.", notesTask.answerNote, notesTask.answerDate)
	if run.answer != wantAnswer {
		t.Fatalf("answer = %q, want %q", run.answer, wantAnswer)
	}

	var tracedCalls []core.ID
	turnIDs := map[core.ID]bool{}
	for _, line := range readTraceLines(t, run.stateDir) {
		turnIDs[line.Event.TurnID] = true
		if line.Type == core.EventToolCallStarted {
			tracedCalls = append(tracedCalls, line.Event.Call.ID)
		}
	}
	if !slices.Equal(tracedCalls, run.calls) || len(turnIDs) != 1 {
		t.Fatalf("trace holds calls %v across %d turns, want %v in one turn", tracedCalls, len(turnIDs), run.calls)
	}
	if !strings.Contains(run.diagnostics, "trace written to") {
		t.Fatalf("diagnostics = %q, want the trace path reported", run.diagnostics)
	}
}

// TestNotesTaskReplaysARecordedModel is the scripted test's recorded twin (TRA-21): the same
// task, but the replies are a real model's, replayed through the real provider adapter and its
// stream parser. Re-record: go test ./cmd/travis -run Recorded -record-cassettes
func TestNotesTaskReplaysARecordedModel(t *testing.T) {
	provider, model := buildRecordedProvider(t, "notes-task.jsonl", "")

	run := runNotesTask(t, provider, model)

	// A re-record can word the answer differently, so only the date is pinned.
	if !strings.Contains(run.answer, "13") || !strings.Contains(run.answer, "October") {
		t.Fatalf("answer = %q, want it to give the appointment date, %s", run.answer, notesTask.answerDate)
	}
	// The cassette is a reasoning model's, so the last step thinks before it answers (TRA-33).
	if run.finalThoughts == "" || strings.Contains(run.answer, run.finalThoughts) {
		t.Fatalf("final thoughts = %q, answer = %q: want reasoning streamed apart from the answer", run.finalThoughts, run.answer)
	}
}

// TestNotesTaskReplaysARecordedGeminiModel guards TRA-39: Gemini sends tool calls without an index
// and needs each call's thought signature back, or the step after a tool call fails with a 400.
// Re-record: go test ./cmd/travis -run RecordedGemini -record-cassettes (needs GEMINI_API_KEY)
func TestNotesTaskReplaysARecordedGeminiModel(t *testing.T) {
	provider, model := buildRecordedProvider(t, "gemini-notes-task.jsonl", "gemini")

	run := runNotesTask(t, provider, model)

	if !strings.Contains(run.answer, "13") || !strings.Contains(run.answer, "October") {
		t.Fatalf("answer = %q, want it to give the appointment date, %s", run.answer, notesTask.answerDate)
	}
}

// buildRecordedProvider replays the named cassette, or records it against the named provider
// profile (the active one when empty) under -record-cassettes. It also reports the model to ask for.
func buildRecordedProvider(t *testing.T, cassetteName, profileName string) (*openaicompat.Provider, string) {
	t.Helper()
	baseURL, model, auth := "http://cassette.invalid/v1", "replayed-model", config.AuthCfg{}
	if cassette.IsRecording() {
		cfg, _, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if profileName == "" {
			profileName = cfg.Provider.Active
		}
		profile := cfg.Provider.Profiles[profileName]
		baseURL, model, auth = profile.BaseURL, profile.Model, profile.Auth
		t.Logf("recording against %s, model %s (provider profile %q)", baseURL, model, profileName)
	}
	path := filepath.Join("..", "..", "testdata", "cassettes", cassetteName)
	return openaicompat.BuildProvider(baseURL, config.ResolveProviderAPIKey(auth), cassette.BuildClient(t, path), core.SystemClock{}), model
}

// TestWriteTaskRunsEndToEndThroughTheRealWiring is the TRA-25 write path: fs_read, fs_edit and
// fs_write through the real registry and LocalBroker on a scratch copy of the notes fixture.
func TestWriteTaskRunsEndToEndThroughTheRealWiring(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "notes")
	if err := os.CopyFS(workspace, os.DirFS(filepath.Join("..", "..", "testdata", "fixtures", "notes"))); err != nil {
		t.Fatal(err)
	}
	const confirmation = "admin/clinic-confirmation.md"
	provider := &scriptedProvider{t: t, steps: []scriptedStep{
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			offered := map[string]bool{}
			for _, spec := range request.Tools {
				offered[spec.Name] = true
			}
			if !offered["fs_write"] || !offered["fs_edit"] {
				t.Fatalf("offered tools %v, want fs_write and fs_edit from the real registry", request.Tools)
			}
			return buildToolCallChunks("call-read", "fs_read", `{"path":"`+confirmation+`"}`)
		},
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			requireContains(t, "fs_read", readLatestToolResult(t, request).Content, "09:30")
			return buildToolCallChunks("call-edit", "fs_edit",
				`{"path":"`+confirmation+`","old_string":"Tuesday 13 October 2026, 09:30\n","new_string":"Tuesday 13 October 2026, 10:15\n"}`)
		},
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			requireContains(t, "fs_edit", readLatestToolResult(t, request).Content, `"bytes_written"`)
			return buildToolCallChunks("call-write", "fs_write", `{"path":"summary.md","content":"Dentist moved to 10:15.\nRoom 4.\n"}`)
		},
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			requireContains(t, "fs_write", readLatestToolResult(t, request).Content, `"created":true`)
			return []core.Chunk{{Text: "Moved the appointment and wrote summary.md.", Finish: core.StopEndTurn}}
		},
	}}

	cfg := &config.Config{Brain: config.BrainCfg{MaxSteps: e2eMaxSteps}, Tools: config.ToolsCfg{MaxExposed: 60}}
	paths := config.PathSet{WorkspaceDir: workspace, StateDir: t.TempDir(), RuntimeDir: buildShortRuntimeDir(t)}
	var diagnostics strings.Builder
	bus := buildSessionBus(io.Discard)
	remote, brokerStateDir := startTestSessionBroker(t, paths, &diagnostics, broker.RemoteOptions{Events: bus})
	session, err := assembleChatSession(chatSessionDeps{
		Cfg: cfg, Paths: paths, Session: testSessionID, Provider: provider, Model: "scripted-model",
		Files: remote, Log: remote, Memory: remote, Commands: remote, Pages: remote,
		Bus: bus, Diagnostics: io.Discard, Redactor: redact.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	turnErr := session.conversation.RunAssistantTurn(context.Background(), "Move my dentist appointment to 10:15 and write a summary.")
	session.close()
	if turnErr != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", turnErr)
	}
	if len(provider.requests) != len(provider.steps) {
		t.Fatalf("took %d provider calls, want %d", len(provider.requests), len(provider.steps))
	}

	edited, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(confirmation)))
	if err != nil {
		t.Fatal(err)
	}
	wantEdited := "Riverside Dental Clinic: booking confirmed\r\nPatient: me\r\nAppointment: Tuesday 13 October 2026, 10:15\r\nWith Dr. Tan, room 4\r\n"
	if string(edited) != wantEdited {
		t.Fatalf("edited file = %q, want %q", edited, wantEdited)
	}
	summary, err := os.ReadFile(filepath.Join(workspace, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	eol := config.ReportNativeLineEnding()
	if want := "Dentist moved to 10:15." + eol + "Room 4." + eol; string(summary) != want {
		t.Fatalf("summary = %q, want %q", summary, want)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 {
		t.Fatalf("scratch root holds %v (%v), want only the workspace", entries, err)
	}
	assertWritesWereBackedUpAndLogged(t, brokerStateDir, workspace, confirmation)
}

// assertWritesWereBackedUpAndLogged reads the broker's own session log: every message travis
// reported, then each write logged by the broker between the call that asked for it and its
// result, with a backup that restores the edited file's original bytes (§5.15.1).
func assertWritesWereBackedUpAndLogged(t *testing.T, brokerStateDir, workspace, edited string) {
	t.Helper()
	sessionLog, err := filestore.Open(filepath.Join(brokerStateDir, "sessions"), testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionLog.Close()
	entries := sessionLog.ReadEntries()
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace) // the broker logs resolved paths
	if err != nil {
		t.Fatal(err)
	}

	var kinds []string
	changes := map[string]*core.FileChange{}
	for _, entry := range entries {
		switch entry.Kind {
		case core.EntryMessage:
			if entry.Author != core.AuthorTravis {
				t.Fatalf("message entry authored by %q, want travis", entry.Author)
			}
			kinds = append(kinds, string(entry.Message.Role))
		case core.EntryFileChanged:
			if entry.Author != core.AuthorBroker || entry.Call == "" {
				t.Fatalf("file_changed entry %+v, want the broker's, naming its call", entry)
			}
			kinds = append(kinds, "write:"+string(entry.Call))
			rel, _ := filepath.Rel(resolvedWorkspace, entry.FileChange.Path)
			changes[filepath.ToSlash(rel)] = entry.FileChange
		case core.EntryPolicyDenied, core.EntryApprovalAsked, core.EntryApprovalGiven, core.EntryFilesReverted, core.EntryRewound:
			kinds = append(kinds, string(entry.Kind))
		}
		if entry.Turn != entries[0].Turn {
			t.Fatalf("entry %+v is not filed under the one turn %q", entry, entries[0].Turn)
		}
	}
	want := []string{"user", "assistant", "tool", "assistant", "write:call-edit", "tool", "assistant", "write:call-write", "tool", "assistant"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("session log = %v\nwant          %v", kinds, want)
	}

	if created := changes["summary.md"]; created == nil || created.Before != "" {
		t.Fatalf("summary.md change = %+v, want one with no backup: the file did not exist", created)
	}
	editedChange := changes[edited]
	if editedChange == nil || editedChange.Before == "" {
		t.Fatalf("%s change = %+v, want a backup of its prior bytes", edited, editedChange)
	}
	backups, err := blobs.Open(filepath.Join(brokerStateDir, "blobs"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	editedPath := filepath.Join(workspace, filepath.FromSlash(edited))
	if err := backups.Restore(context.Background(), editedPath, blobs.Ref(editedChange.Before)); err != nil {
		t.Fatalf("restore the backup: %v", err)
	}
	original, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "notes", filepath.FromSlash(edited)))
	if err != nil {
		t.Fatal(err)
	}
	if restored, err := os.ReadFile(editedPath); err != nil || string(restored) != string(original) {
		t.Fatalf("restored %s = %q, %v; want the fixture's original bytes", edited, restored, err)
	}
}

// TestUndoRewindsFilesAndConversationThroughTheRealBroker is TRA-31's /undo end to end: two turns
// write through the real broker process, /undo takes both back, and the next request no longer
// carries them; the session log and the trace both record the rewind.
func TestUndoRewindsFilesAndConversationThroughTheRealBroker(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "a.txt"), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reply := func(text string) scriptedStep {
		return func(*testing.T, core.ProviderRequest) []core.Chunk {
			return []core.Chunk{{Text: text, Finish: core.StopEndTurn}}
		}
	}
	write := func(id core.ID, path, content string) scriptedStep {
		return func(*testing.T, core.ProviderRequest) []core.Chunk {
			return buildToolCallChunks(id, "fs_write", `{"path":"`+path+`","content":"`+content+`"}`)
		}
	}
	provider := &scriptedProvider{t: t, steps: []scriptedStep{
		write("call-a", "a.txt", "changed"), reply("edited a.txt"),
		write("call-new", "new.txt", "brand new"), reply("created new.txt"),
		func(t *testing.T, request core.ProviderRequest) []core.Chunk {
			if len(request.Messages) != 1 {
				t.Fatalf("request after /undo carries %d messages, want only the new prompt", len(request.Messages))
			}
			return []core.Chunk{{Text: "starting over", Finish: core.StopEndTurn}}
		},
	}}

	cfg := &config.Config{Brain: config.BrainCfg{MaxSteps: e2eMaxSteps}, Tools: config.ToolsCfg{MaxExposed: 60}, Eval: config.EvalCfg{Traces: true}}
	paths := config.PathSet{WorkspaceDir: workspace, StateDir: t.TempDir(), RuntimeDir: buildShortRuntimeDir(t)}
	var diagnostics strings.Builder
	bus := buildSessionBus(io.Discard)
	remote, brokerStateDir := startTestSessionBroker(t, paths, &diagnostics, broker.RemoteOptions{Events: bus})
	session, err := assembleChatSession(chatSessionDeps{
		Cfg: cfg, Paths: paths, Session: testSessionID, Provider: provider, Model: "scripted-model",
		Files: remote, Log: remote, Memory: remote, Commands: remote, Pages: remote,
		Bus: bus, Diagnostics: io.Discard, Redactor: redact.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, prompt := range []string{"change a.txt", "add new.txt"} {
		if err := session.conversation.RunAssistantTurn(ctx, prompt); err != nil {
			t.Fatalf("%s: %v", prompt, err)
		}
	}

	turns, err := session.conversation.ListTurns(ctx)
	if err != nil || len(turns) != 2 || turns[0].Prompt != "change a.txt" || !slices.Equal(turns[1].ChangedPaths, []string{"new.txt"}) {
		t.Fatalf("ListTurns = %+v, %v", turns, err)
	}
	report, err := session.conversation.Rewind(ctx, turns[0].Turn, core.RewindFilesAndConversation)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(report.Files.Restored, []string{"a.txt"}) || !slices.Equal(report.Files.Deleted, []string{"new.txt"}) {
		t.Fatalf("report = %+v, want a.txt restored and new.txt deleted", report)
	}
	if got, err := os.ReadFile(filepath.Join(workspace, "a.txt")); err != nil || string(got) != "original\n" {
		t.Fatalf("a.txt = %q, %v; want its original bytes", got, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new.txt still exists: %v", err)
	}
	if err := session.conversation.RunAssistantTurn(ctx, "start over"); err != nil {
		t.Fatal(err)
	}
	session.close()

	assertRewindWasLoggedAndTraced(t, brokerStateDir, paths.StateDir)
}

func assertRewindWasLoggedAndTraced(t *testing.T, brokerStateDir, travisStateDir string) {
	t.Helper()
	sessionLog, err := filestore.Open(filepath.Join(brokerStateDir, "sessions"), testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionLog.Close()
	var rewindKinds []core.SessionEntryKind
	for _, entry := range sessionLog.ReadEntries() {
		if entry.Kind == core.EntryFilesReverted || entry.Kind == core.EntryRewound {
			if entry.Author != core.AuthorBroker {
				t.Fatalf("%s authored by %q, want the broker", entry.Kind, entry.Author)
			}
			rewindKinds = append(rewindKinds, entry.Kind)
		}
	}
	if !slices.Equal(rewindKinds, []core.SessionEntryKind{core.EntryFilesReverted, core.EntryRewound}) {
		t.Fatalf("rewind entries = %v, want files_reverted then rewound", rewindKinds)
	}

	trace, err := os.ReadFile(filepath.Join(travisStateDir, "traces", string(testSessionID)+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []core.EventType{core.EventFileChanged, core.EventFilesReverted, core.EventConversationRewound} {
		if !strings.Contains(string(trace), `"`+string(want)+`"`) {
			t.Fatalf("trace lacks %s, which undo_rate is counted from", want)
		}
	}
}
