// Package terminal is the outer adapter for the text frontend: it collects user intent
// and renders observed facts. It knows nothing about providers, tools, policy, or the agent loop.
package terminal

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	quitCommand = "/quit"
	// coalesceWindow turns a token stream into at most one render per frame, 30 fps (DESIGN.md §5.9.1, R-07).
	coalesceWindow = 33 * time.Millisecond
	// Used until the terminal reports its size, and for outputs that never do.
	defaultWidth  = 80
	defaultHeight = 24
	// chromeHeight is the two separators, the input line, and the status bar below the transcript.
	chromeHeight = 4
)

// ErrNotInteractive is returned when stdin or stdout is not a terminal; the TUI cannot run there.
var ErrNotInteractive = errors.New("chat needs an interactive terminal (non-interactive mode is not built yet)")

// Options carries the runtime choices for one chat session.
type Options struct {
	ModelName       string
	StartupWarnings []string
	Input           io.Reader
	Output          io.Writer
}

// RunChat runs the interactive chat until the user quits or input ends. runTurn is the
// assistant-turn operation; the frontend calls it without knowing what implements it.
// approvals delivers pending Ask decisions for the modal to answer (D-10).
func RunChat(
	ctx context.Context,
	events <-chan core.Event,
	approvals <-chan core.ApprovalRequest,
	runTurn func(ctx context.Context, prompt string) error,
	opts Options,
) error {
	if !isTerminal(opts.Input) || !isTerminal(opts.Output) {
		return ErrNotInteractive
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // also stops a turn still running when the program exits

	input, inputEnded := watchInputEnd(opts.Input)
	// Bubble Tea recovers panics in View, Update, and Cmds, restores the terminal, and prints the
	// trace (DESIGN.md §5.9); passing tea.WithoutCatchPanics would give that up.
	program := tea.NewProgram(
		buildChatModel(ctx, events, approvals, runTurn, opts),
		tea.WithInput(input),
		tea.WithOutput(opts.Output),
	)
	go func() {
		select {
		case <-inputEnded:
			program.Send(inputEndedMsg{})
		case <-ctx.Done():
		}
	}()

	_, err := program.Run()
	if errors.Is(err, tea.ErrInterrupted) {
		return nil
	}
	return err
}

// watchInputEnd reports when non-terminal input runs out, because Bubble Tea keeps running
// after EOF. A terminal is returned untouched so Bubble Tea can still switch it to raw mode.
func watchInputEnd(input io.Reader) (io.Reader, <-chan struct{}) {
	if isTerminal(input) {
		return input, nil
	}
	reader := &endReportingReader{reader: input, ended: make(chan struct{})}
	return reader, reader.ended
}

func isTerminal(stream any) bool {
	file, isFile := stream.(term.File)
	return isFile && term.IsTerminal(file.Fd())
}

type endReportingReader struct {
	reader io.Reader
	ended  chan struct{}
	once   sync.Once
}

func (r *endReportingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil {
		r.once.Do(func() { close(r.ended) })
	}
	return n, err
}

type eventBatchMsg struct {
	events          []core.Event
	isChannelClosed bool
}

type turnFinishedMsg struct {
	turnNumber int
	err        error
}

type inputEndedMsg struct{}

type approvalRequestedMsg struct{ request core.ApprovalRequest }

func listenForEvents(events <-chan core.Event) tea.Cmd {
	return func() tea.Msg { return collectEventBatch(events, coalesceWindow) }
}

func listenForApprovals(approvals <-chan core.ApprovalRequest) tea.Cmd {
	return func() tea.Msg {
		request, isOpen := <-approvals
		if !isOpen {
			return nil
		}
		return approvalRequestedMsg{request: request}
	}
}

// collectEventBatch waits for one event, then gathers whatever else arrives within the window.
// A completed turn flushes at once so the final words never wait on the timer.
func collectEventBatch(events <-chan core.Event, window time.Duration) eventBatchMsg {
	first, isOpen := <-events
	if !isOpen {
		return eventBatchMsg{isChannelClosed: true}
	}
	batch := eventBatchMsg{events: []core.Event{first}}
	if _, isTurnEnd := first.(core.TurnCompleted); isTurnEnd {
		return batch
	}

	timer := time.NewTimer(window)
	defer timer.Stop()
	for {
		select {
		case event, isOpen := <-events:
			if !isOpen {
				batch.isChannelClosed = true
				return batch
			}
			batch.events = append(batch.events, event)
			if _, isTurnEnd := event.(core.TurnCompleted); isTurnEnd {
				return batch
			}
		case <-timer.C:
			return batch
		}
	}
}

type chatModel struct {
	ctx       context.Context
	events    <-chan core.Event
	approvals <-chan core.ApprovalRequest
	runTurn   func(context.Context, string) error
	modelName string

	transcript      transcript
	viewport        viewport.Model
	input           textinput.Model
	width           int
	pendingApproval *core.ApprovalRequest
	// brokerFailure is set once the broker connection breaks; it never clears, since every
	// later effect fails closed until travis restarts.
	brokerFailure string

	cancelTurn        context.CancelFunc // set only while a turn runs
	isCancelRequested bool
	hasInputEnded     bool
	lastTurnOutcome   turnOutcome
}

func buildChatModel(
	ctx context.Context,
	events <-chan core.Event,
	approvals <-chan core.ApprovalRequest,
	runTurn func(context.Context, string) error,
	opts Options,
) chatModel {
	input := textinput.New()
	input.Prompt = " › "
	input.SetVirtualCursor(false)
	input.Focus()

	m := chatModel{
		ctx: ctx, events: events, approvals: approvals, runTurn: runTurn, modelName: opts.ModelName,
		viewport: viewport.New(), input: input,
	}
	m.transcript.warnings = opts.StartupWarnings
	m.resize(defaultWidth, defaultHeight)
	return m
}

func (m chatModel) Init() tea.Cmd {
	return tea.Batch(listenForEvents(m.events), listenForApprovals(m.approvals))
}

func (m chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseWheelMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case eventBatchMsg:
		for _, event := range msg.events {
			if unavailable, isUnavailable := event.(core.BrokerUnavailable); isUnavailable {
				m.brokerFailure = unavailable.Reason
			}
			m.transcript.applyEvent(event)
		}
		m.refreshTranscript()
		if !msg.isChannelClosed {
			return m, listenForEvents(m.events)
		}
	case approvalRequestedMsg:
		m.pendingApproval = &msg.request
		// Not re-armed here: only one modal at a time; handleApprovalKey re-arms it once answered.
		return m, nil
	case turnFinishedMsg:
		return m.finishTurn(msg)
	case inputEndedMsg:
		m.hasInputEnded = true
		if m.cancelTurn == nil {
			return m, tea.Quit
		}
	}
	return m, nil
}

// handleKey routes keys: only PgUp/PgDn reach the transcript, so typing never scrolls it.
// A pending approval takes every key first, so nothing typed while the modal is up can
// leak through to the input or the transcript.
func (m chatModel) handleKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.pendingApproval != nil {
		return m.handleApprovalKey(key)
	}
	isTurnRunning := m.cancelTurn != nil
	switch key.String() {
	case "ctrl+c":
		if !isTurnRunning {
			return m, tea.Quit
		}
		m.requestCancel()
		return m, nil
	case "esc":
		if isTurnRunning {
			m.requestCancel()
		}
		return m, nil
	case "ctrl+d":
		if !isTurnRunning && m.input.Value() == "" {
			return m, tea.Quit
		}
	case "enter":
		if isTurnRunning {
			return m, nil
		}
		return m.submitPrompt()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(key)
		return m, cmd
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	return m, cmd
}

// handleApprovalKey answers the pending approval (D-10, keyboard-only): y/a/n are the
// only decisions, anything else is swallowed. This is the one place in the program that
// can produce ApprovalAlways, and it is reachable only from a real tea.KeyPressMsg the
// terminal generated — never from model output.
func (m chatModel) handleApprovalKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var decision core.ApprovalDecision
	switch key.String() {
	case "y":
		decision = core.ApprovalOnce
	case "a":
		decision = core.ApprovalAlways
	case "n", "esc":
		decision = core.ApprovalDenied
	default:
		return m, nil
	}

	respond := m.pendingApproval.Respond
	m.pendingApproval = nil
	return m, tea.Batch(
		func() tea.Msg { respond(decision); return nil },
		listenForApprovals(m.approvals),
	)
}

func (m chatModel) submitPrompt() (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(m.input.Value())
	m.input.Reset()
	switch prompt {
	case "":
		return m, nil
	case quitCommand:
		return m, tea.Quit
	}

	turnCtx, cancel := context.WithCancel(m.ctx)
	m.cancelTurn, m.isCancelRequested = cancel, false
	turnNumber := m.transcript.startTurn(prompt)
	m.refreshTranscript()
	m.viewport.GotoBottom()

	runTurn := m.runTurn
	return m, func() tea.Msg {
		return turnFinishedMsg{turnNumber: turnNumber, err: runTurn(turnCtx, prompt)}
	}
}

func (m *chatModel) requestCancel() {
	m.cancelTurn()
	m.isCancelRequested = true
}

func (m chatModel) finishTurn(msg turnFinishedMsg) (tea.Model, tea.Cmd) {
	m.cancelTurn()
	m.cancelTurn = nil

	outcome, failure := turnFailed, ""
	switch {
	case msg.err == nil:
		outcome = turnSucceeded
	case m.isCancelRequested:
		outcome = turnInterrupted
	default:
		failure = msg.err.Error()
	}
	m.lastTurnOutcome = outcome
	m.transcript.finishTurn(msg.turnNumber, outcome, failure)
	m.refreshTranscript()

	if m.hasInputEnded {
		return m, tea.Quit
	}
	return m, nil
}

func (m *chatModel) resize(width, height int) {
	m.width = max(width, 1)
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(max(height-chromeHeight, 1))
	m.input.SetWidth(max(m.width-lipgloss.Width(m.input.Prompt)-1, 1))
	m.refreshTranscript()
}

// refreshTranscript re-renders the transcript, following new output unless the user scrolled up.
func (m *chatModel) refreshTranscript() {
	isFollowingLatest := m.viewport.AtBottom()
	m.viewport.SetContent(renderTranscript(m.transcript, m.width))
	if isFollowingLatest {
		m.viewport.GotoBottom()
	}
}
