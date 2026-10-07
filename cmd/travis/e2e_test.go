package main

import (
	"bufio"
	"context"
	"encoding/json"
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
	calls       []core.ID
	answer      string
	stateDir    string
	diagnostics string
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
	paths := config.PathSet{WorkspaceDir: workspace, StateDir: t.TempDir()}
	cfg := &config.Config{
		Brain: config.BrainCfg{MaxSteps: e2eMaxSteps},
		Tools: config.ToolsCfg{MaxExposed: 60},
		Eval:  config.EvalCfg{Traces: true},
	}
	var diagnostics strings.Builder
	session, err := assembleChatSession(cfg, paths, provider, model, &diagnostics)
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
	var answer strings.Builder
	var completed *core.TurnCompleted
	for event := range events {
		switch fact := event.(type) {
		case core.ToolCallStarted:
			started = append(started, fact.Call.ID)
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
	return notesTaskRun{calls: started, answer: answer.String(), stateDir: paths.StateDir, diagnostics: diagnostics.String()}
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
	path := filepath.Join("..", "..", "testdata", "cassettes", "notes-task.jsonl")
	baseURL, model := "http://cassette.invalid/v1", "replayed-model"
	if cassette.IsRecording() {
		cfg, _, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		profile := cfg.Provider.Profiles[cfg.Provider.Active]
		baseURL, model = profile.BaseURL, profile.Model
		t.Logf("recording against %s, model %s (provider profile %q)", baseURL, model, cfg.Provider.Active)
	}
	provider := openaicompat.BuildProvider(baseURL, config.ResolveProviderAPIKey(), cassette.BuildClient(t, path), core.SystemClock{})

	run := runNotesTask(t, provider, model)

	// A re-record can word the answer differently, so only the date is pinned.
	if !strings.Contains(run.answer, "13") || !strings.Contains(run.answer, "October") {
		t.Fatalf("answer = %q, want it to give the appointment date, %s", run.answer, notesTask.answerDate)
	}
}
