// Package terminal is the outer adapter for the text frontend: it collects user intent
// and renders observed facts. It knows nothing about providers, tools, policy, or the agent loop.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	// coalesceWindow turns a token stream into at most one render per frame, 30 fps (DESIGN.md §5.9.1, R-07).
	coalesceWindow = 33 * time.Millisecond
	// Used until the terminal reports its size, and for outputs that never do.
	defaultWidth  = 80
	defaultHeight = 24
	// chromeHeight is the two separators, the input line, and the status bar below the transcript.
	chromeHeight = 4
	// ponytail: prefix filter capped at a few rows; fuzzy matching when there are >20 commands.
	maxPaletteRows = 8
)

// ErrNotInteractive is returned when stdin or stdout is not a terminal; the TUI cannot run there.
var ErrNotInteractive = errors.New("chat needs an interactive terminal (non-interactive mode is not built yet)")

// Options carries the runtime choices for one chat session.
type Options struct {
	ModelName string
	// Clock drives elapsed-turn timing and animation ticks.
	Clock core.Clock
	// Theme is auto, dark, light, high_contrast, or mono. ReducedMotion keeps every mascot static.
	Theme         string
	ReducedMotion bool
	// ContextLength is the model's context window in tokens for the status bar; 0 means unknown.
	ContextLength int
	// MaxTurnCostUSD and MaxDailyCostUSD are the spending ceilings for the status bar; 0 means off.
	MaxTurnCostUSD, MaxDailyCostUSD float64
	// SpentTodayUSD is today's spend before this session's first turn.
	SpentTodayUSD float64
	// IsPriced is true when the active profile has a per-token price, so the status bar and
	// /cost may show a dollar figure even with no spending ceiling configured.
	IsPriced bool
	// History is a resumed or forked session's visible conversation, shown as its earlier turns.
	History         []RestoredMessage
	StartupWarnings []string
	Input           io.Reader
	Output          io.Writer
	Questions       <-chan core.QuestionRequest
	// Diagnostics, when set, shows its lines as notices while the chat runs.
	Diagnostics *DiagnosticsWriter
	// PushToTalkKey records while held; empty means ctrl+t (§5.10 ptt_key).
	PushToTalkKey        string
	IsVoiceInputEnabled  bool
	IsVoiceOutputEnabled bool
}

// RestoredMessage is one logged message of a resumed session, with the turn it belongs to.
type RestoredMessage struct {
	Turn    core.ID
	Message core.Message
}

// UndoActions are the operations /undo runs: list the turns it can take back, and rewind from
// one of them. With either nil, /undo says it is unavailable.
type UndoActions struct {
	ListTurns func(ctx context.Context) ([]core.TurnSummary, error)
	Rewind    func(ctx context.Context, fromTurn core.ID, mode core.RewindMode) (core.RewindReport, error)
}

// SkillSummary is one loaded skill's identity for /skills: plain strings only, so the
// terminal package never has to import internal/tools/skills to render it.
type SkillSummary struct {
	Name, Tier, Description string
}

// VoiceConfiguration is the effective voice state after a configuration change.
type VoiceConfiguration struct {
	IsInputEnabled  bool
	IsOutputEnabled bool
	PushToTalkKey   string
	StartDictation  func(ctx context.Context) (finish func() (string, error), err error)
}

// VoiceChange changes exactly one voice setting.
type VoiceChange struct {
	InputEnabled  *bool
	OutputEnabled *bool
	PushToTalkKey *string
}

// ChatActions are the operations the chat can ask of the session; the frontend calls them
// without knowing what implements them.
type ChatActions struct {
	RunTurn      func(ctx context.Context, prompt string) error
	ResetHistory func()
	SelectModel  func(name string)
	// SwitchProvider moves later turns onto the named provider profile, or reports the key it
	// still needs; SwitchProviderWithKey stores that key first (empty switches without one).
	SwitchProvider        func(profile string) (ProviderSwitch, error)
	SwitchProviderWithKey func(profile, apiKey string) (ProviderSwitch, error)
	ListProviders         func() []string
	// DescribeProviders reports each profile with whether it has a key; it may read the OS
	// credential store, so it runs off the UI thread. ListMCPServers is live, like ListTools.
	DescribeProviders func() []ProviderSummary
	ListMCPServers    func() []MCPServerSummary
	// ListModels names the models the active endpoint offers, for /model suggestions; nil means none.
	ListModels func(ctx context.Context) ([]string, error)
	// ListTools and ListSkills query the session live, not a startup snapshot.
	ListTools  func() []string
	ListSkills func() []SkillSummary
	Undo       UndoActions
	// StartDictation begins a push-to-talk recording; finish stops it and returns the transcript.
	// Nil means voice input is off.
	StartDictation func(ctx context.Context) (finish func() (string, error), err error)
	// ConfigureVoice applies a voice change now and persists it to the user config.
	ConfigureVoice func(change VoiceChange) (VoiceConfiguration, error)
	Inspect        InspectionActions
	Memory         MemoryActions
	Settings       SettingsActions
}

// RunChat runs the interactive chat until the user quits or input ends.
// approvals delivers pending Ask decisions for the modal to answer (D-10).
func RunChat(
	ctx context.Context,
	events <-chan core.Event,
	approvals <-chan core.ApprovalRequest,
	actions ChatActions,
	opts Options,
) error {
	if !isTerminal(opts.Input) || !isTerminal(opts.Output) {
		return ErrNotInteractive
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // also stops a turn still running when the program exits

	input, inputEnded := watchInputEnd(opts.Input)
	model := buildChatModel(ctx, events, approvals, actions, opts)
	if opts.Diagnostics != nil {
		model.diagnostics = opts.Diagnostics.startCapture()
		defer opts.Diagnostics.stopCapture()
	}
	// Bubble Tea recovers panics in View, Update, and Cmds, restores the terminal, and prints the
	// trace (DESIGN.md §5.9); passing tea.WithoutCatchPanics would give that up.
	program := tea.NewProgram(
		model,
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

// modelsListedMsg carries /model suggestions; a failed listing is dropped, leaving no suggestions.
type modelsListedMsg struct{ names []string }

func listModels(ctx context.Context, list func(context.Context) ([]string, error)) tea.Cmd {
	return func() tea.Msg {
		names, err := list(ctx)
		if err != nil {
			return nil
		}
		return modelsListedMsg{names: names}
	}
}

type approvalRequestedMsg struct{ request core.ApprovalRequest }
type questionRequestedMsg struct{ request core.QuestionRequest }

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

func listenForQuestions(questions <-chan core.QuestionRequest) tea.Cmd {
	if questions == nil {
		return nil
	}
	return func() tea.Msg {
		request, isOpen := <-questions
		if !isOpen {
			return nil
		}
		return questionRequestedMsg{request: request}
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
	questions <-chan core.QuestionRequest
	// diagnostics are runtime diagnostic lines, shown as notices instead of drawn over the screen.
	diagnostics <-chan string
	actions     ChatActions
	commands    []slashCommand
	modelName   string
	// contextLength is the active model's window; contextTokens is the size of the latest provider call's input.
	contextLength, contextTokens int
	// maxTurnCostUSD and maxDailyCostUSD are the ceilings; turnCostUSD and spentTodayUSD are the latest turn's spend and the day's total.
	maxTurnCostUSD, maxDailyCostUSD, turnCostUSD, spentTodayUSD float64
	// isPriced is true when the active profile has a per-token price (terminal.Options.IsPriced).
	isPriced bool
	// modelNames are the /model suggestions, empty until the endpoint's listing arrives.
	modelNames []string

	transcript      transcript
	replies         *replyRenderer // a pointer so its cache survives chatModel's value copies
	viewport        viewport.Model
	input           textinput.Model
	width           int
	height          int
	paletteIndex    int
	selection       textSelection
	pendingApproval *core.ApprovalRequest
	pendingQuestion *core.QuestionRequest
	// undoPicker is open while the user chooses what /undo takes back; it takes every key.
	undoPicker *undoPicker
	// keyPromptProfile names the provider profile whose API key the masked input is collecting.
	keyPromptProfile string
	confirmation     *confirmation
	memoryEditor     *memoryEditor
	settingsPanel    *settingsPanel
	commandPanel     *commandPanel
	// brokerFailure is set once the broker connection breaks; it never clears, since every
	// later effect fails closed until travis restarts.
	brokerFailure string
	// sandboxTier is the tier MCP servers run under, empty until one starts (DESIGN §9.2.1: always shown).
	sandboxTier string
	// unavailableMCPServers holds servers that failed to start or ran out of restarts, until a later start clears them.
	unavailableMCPServers map[string]bool

	dictation       dictationState
	finishDictation func() (string, error) // set only while recording
	// isDictationStopRequested remembers a release that came before the recording had started.
	isDictationStopRequested bool
	// canHoldToTalk is true once the terminal reports key releases; until then push-to-talk toggles.
	canHoldToTalk        bool
	pushToTalkKey        string
	pushToTalkCode       rune
	isVoiceInputEnabled  bool
	isVoiceOutputEnabled bool
	isVoiceConfiguring   bool

	cancelTurn           context.CancelFunc // set only while a turn runs
	isCancelRequested    bool
	hasInputEnded        bool
	lastTurnOutcome      turnOutcome
	themePreference      string
	theme                terminalTheme
	isDarkBackground     bool
	isReducedMotion      bool
	mascotFrame          int
	startupFrames        int
	settleFrames         int
	isMascotBlinking     bool
	isMascotTickArmed    bool
	mascotTickInterval   time.Duration
	mascotTickGeneration uint64
	clock                core.Clock
	turnStartedAt        time.Time
	turnElapsed          time.Duration
	activeTurnUsage      core.Usage
}

func buildChatModel(
	ctx context.Context,
	events <-chan core.Event,
	approvals <-chan core.ApprovalRequest,
	actions ChatActions,
	opts Options,
) chatModel {
	input := textinput.New()
	input.Prompt = " › "
	input.SetVirtualCursor(false)
	input.Focus()

	themePreference := opts.Theme
	if themePreference == "" {
		themePreference = themeAuto
	}
	clock := opts.Clock
	if clock == nil {
		clock = core.SystemClock{}
	}
	m := chatModel{
		ctx: ctx, events: events, approvals: approvals, questions: opts.Questions, actions: actions, commands: buildSlashCommands(), modelName: opts.ModelName, contextLength: opts.ContextLength,
		maxTurnCostUSD: opts.MaxTurnCostUSD, maxDailyCostUSD: opts.MaxDailyCostUSD, spentTodayUSD: opts.SpentTodayUSD, isPriced: opts.IsPriced,
		viewport: viewport.New(), input: input, replies: &replyRenderer{}, pushToTalkKey: opts.PushToTalkKey,
		isVoiceInputEnabled: opts.IsVoiceInputEnabled, isVoiceOutputEnabled: opts.IsVoiceOutputEnabled,
		themePreference: themePreference, theme: buildTerminalTheme(themePreference), isDarkBackground: true, isReducedMotion: opts.ReducedMotion,
		clock: clock,
	}
	if m.pushToTalkKey == "" {
		m.pushToTalkKey = defaultPushToTalkKey
	}
	m.transcript.warnings = opts.StartupWarnings
	m.transcript.restoreTurns(opts.History)
	if len(opts.History) == 0 && !opts.ReducedMotion {
		if len(opts.StartupWarnings) == 0 {
			m.startupFrames = startupFrameCount
		}
		m.isMascotTickArmed = true
		m.mascotTickInterval = m.reportMascotTickInterval()
		m.mascotTickGeneration = 1
	}
	m.applyTheme(m.theme)
	m.resize(defaultWidth, defaultHeight)
	return m
}

func (m chatModel) Init() tea.Cmd {
	cmds := []tea.Cmd{listenForEvents(m.events), listenForApprovals(m.approvals)}
	if m.themePreference == themeAuto {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	if m.isMascotTickArmed {
		cmds = append(cmds, scheduleMascotTick(m.clock, m.mascotTickInterval, m.mascotTickGeneration))
	}
	if m.questions != nil {
		cmds = append(cmds, listenForQuestions(m.questions))
	}
	if m.diagnostics != nil {
		cmds = append(cmds, listenForDiagnostics(m.diagnostics))
	}
	if m.actions.ListModels != nil {
		cmds = append(cmds, listModels(m.ctx, m.actions.ListModels))
	}
	return tea.Batch(cmds...)
}

func (m chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
	case tea.ColorProfileMsg:
		// Markdown is the default until this arrives (once at startup), so tests need no message to be deterministic.
		m.replies.isPlainTextOnly = msg.Profile < colorprofile.ANSI256
		m.refreshTranscript()
	case tea.BackgroundColorMsg:
		m.isDarkBackground = msg.IsDark()
		if m.themePreference == themeAuto {
			if m.isDarkBackground {
				m.applyTheme(buildTerminalTheme(themeDark))
			} else {
				m.applyTheme(buildTerminalTheme(themeLight))
			}
			m.refreshTranscript()
		}
	case mascotTickMsg:
		if msg.generation != 0 && msg.generation != m.mascotTickGeneration {
			return m, nil
		}
		m.isMascotTickArmed = false
		m.mascotTickInterval = 0
		tickedAt := msg.at
		if tickedAt.IsZero() {
			tickedAt = m.clock.ReportCurrentTime()
		}
		m.updateTurnElapsed(tickedAt)
		if m.shouldAnimateMascot() {
			m.isMascotBlinking = false
			m.mascotFrame++
			if m.startupFrames > 0 {
				m.startupFrames--
			}
			if m.settleFrames > 0 {
				m.settleFrames--
			}
		} else if m.isMascotBlinking {
			m.isMascotBlinking = false
			m.mascotFrame = 2
		} else if m.shouldBlinkMascot() {
			m.isMascotBlinking = true
			m.mascotFrame = 1
		}
		m.refreshTranscript()
		cmd := m.armMascotAnimation(nil)
		return m, cmd
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.KeyReleaseMsg:
		return m.handlePushToTalkRelease(msg)
	case tea.KeyboardEnhancementsMsg:
		m.canHoldToTalk = msg.SupportsEventTypes()
	case dictationStartedMsg:
		return m.handleDictationStarted(msg)
	case dictationFinishedMsg:
		return m.handleDictationFinished(msg)
	case voiceConfiguredMsg:
		return m.finishVoiceConfiguration(msg)
	case inspectionFinishedMsg:
		return m.finishInspection(msg)
	case permissionRevokedMsg:
		return m.finishPermissionRevocation(msg)
	case memoryReadMsg:
		return m.finishMemoryRead(msg)
	case memoryChangedMsg:
		return m.finishMemoryChange(msg)
	case settingsLoadedMsg:
		return m.finishSettingsLoad(msg)
	case settingAppliedMsg:
		return m.finishSettingApply(msg)
	case tea.PasteMsg:
		return m.handlePaste(msg)
	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg:
		return m.handleMouseSelection(msg.(tea.MouseMsg))
	case tea.MouseWheelMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case eventBatchMsg:
		for _, event := range msg.events {
			if unavailable, isUnavailable := event.(core.BrokerUnavailable); isUnavailable {
				m.brokerFailure = unavailable.Reason
			}
			if stepDone, isStepDone := event.(core.StepCompleted); isStepDone {
				m.contextTokens = stepDone.Usage.InputTokens
				if m.cancelTurn != nil {
					m.activeTurnUsage.InputTokens += stepDone.Usage.InputTokens
					m.activeTurnUsage.OutputTokens += stepDone.Usage.OutputTokens
				}
			}
			if turnDone, isTurnDone := event.(core.TurnCompleted); isTurnDone {
				m.turnCostUSD, m.spentTodayUSD = turnDone.CostUSD, turnDone.SpentTodayUSD
				if m.cancelTurn != nil {
					m.activeTurnUsage = turnDone.Usage
				}
			}
			if started, isStarted := event.(core.MCPServerStarted); isStarted {
				m.sandboxTier = recordSandboxTier(m.sandboxTier, started.Tier)
				delete(m.unavailableMCPServers, started.Server)
			}
			if unavailable, isUnavailable := event.(core.MCPServerUnavailable); isUnavailable {
				if m.unavailableMCPServers == nil {
					m.unavailableMCPServers = map[string]bool{}
				}
				m.unavailableMCPServers[unavailable.Server] = true
			}
			m.transcript.applyEvent(event)
		}
		m.refreshTranscript()
		if !msg.isChannelClosed {
			return m, listenForEvents(m.events)
		}
	case diagnosticLineMsg:
		m.transcript.addNotice(strings.TrimPrefix(msg.line, "travis: "))
		m.refreshTranscript()
		return m, listenForDiagnostics(m.diagnostics)
	case approvalRequestedMsg:
		m.pendingApproval = &msg.request
		// Not re-armed here: only one modal at a time; handleApprovalKey re-arms it once answered.
		return m.muteDictationForModal()
	case questionRequestedMsg:
		if m.cancelTurn == nil {
			return m, listenForQuestions(m.questions)
		}
		m.pendingQuestion = &msg.request
		m.input.Prompt = " ? "
		m.input.Reset()
		m.refreshTranscript()
		m.viewport.GotoBottom()
		return m.muteDictationForModal()
	case turnFinishedMsg:
		return m.finishTurn(msg)
	case modelsListedMsg:
		m.modelNames = msg.names
		m.fitViewport() // the palette may already be open for "/model "
	case providerSwitchedMsg:
		return m.finishProviderSwitch(msg)
	case providersDescribedMsg:
		return m.showProviderDescriptions(msg)
	case turnsListedMsg:
		return m.openUndoPicker(msg)
	case rewindFinishedMsg:
		return m.finishRewind(msg)
	case inputEndedMsg:
		m.hasInputEnded = true
		if m.cancelTurn == nil {
			return m, tea.Quit
		}
	}
	return m, nil
}

// handlePaste puts bracketed-paste text in the prompt. A modal swallows it instead of letting
// it through: an approval is answered by a real keypress and nothing else (D-10), and pasted
// text landing in a prompt hidden behind a modal is text the user cannot see they are about
// to send.
func (m chatModel) handlePaste(paste tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.memoryEditor != nil {
		editor := *m.memoryEditor
		var cmd tea.Cmd
		editor.textarea, cmd = editor.textarea.Update(paste)
		m.memoryEditor = &editor
		return m, cmd
	}
	if m.settingsPanel != nil {
		panel := *m.settingsPanel
		var cmd tea.Cmd
		if panel.isEditing {
			panel.editor, cmd = panel.editor.Update(paste)
		} else {
			panel.search, cmd = panel.search.Update(paste)
		}
		m.settingsPanel = &panel
		return m, cmd
	}
	if m.pendingApproval != nil || m.pendingQuestion != nil || m.undoPicker != nil || m.confirmation != nil ||
		m.commandPanel != nil {
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(paste)
	if m.keyPromptProfile != "" {
		return m, cmd // a pasted key stays masked; the palette never opens over it
	}
	m.paletteIndex = 0
	m.fitViewport()
	return m, cmd
}

// handleKey routes keys: only PgUp/PgDn reach the transcript, so typing never scrolls it.
// A pending approval takes every key first, so nothing typed while the modal is up can
// leak through to the input or the transcript.
func (m chatModel) handleKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.pendingApproval != nil {
		return m.handleApprovalKey(key)
	}
	if m.pendingQuestion != nil {
		return m.handleQuestionKey(key)
	}
	if m.undoPicker != nil {
		return m.handleUndoPickerKey(key)
	}
	if m.confirmation != nil {
		return m.handleConfirmationKey(key)
	}
	if m.memoryEditor != nil {
		return m.handleMemoryEditorKey(key)
	}
	if m.settingsPanel != nil {
		return m.handleSettingsKey(key)
	}
	if m.commandPanel != nil {
		return m.handleCommandPanelKey(key)
	}
	if m.keyPromptProfile != "" {
		return m.handleKeyPromptKey(key)
	}
	if m.isDictationAvailable() && key.String() == m.pushToTalkKey {
		return m.handlePushToTalkPress(key)
	}
	if rows := m.listPaletteRows(); len(rows) > 0 {
		switch key.String() {
		case "up":
			m.paletteIndex = max(m.paletteIndex-1, 0)
			return m, nil
		case "down":
			m.paletteIndex = min(m.paletteIndex+1, len(rows)-1)
			return m, nil
		case "tab":
			m.input.SetValue(rows[m.paletteIndex].completion)
			m.input.CursorEnd()
			m.paletteIndex = 0
			m.fitViewport()
			return m, nil
		case "enter":
			m.input.SetValue(rows[m.paletteIndex].completion)
			return m.submitPrompt()
		case "esc":
			m.input.Reset()
			m.paletteIndex = 0
			m.fitViewport()
			return m, nil
		}
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

	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	if m.input.Value() != before {
		m.paletteIndex = 0
		m.fitViewport()
	}
	return m, cmd
}

func (m chatModel) handleQuestionKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "ctrl+c":
		m.requestCancel()
		m.pendingQuestion = nil
		m.input.Reset()
		m.input.Prompt = " › "
		m.refreshTranscript()
		return m, listenForQuestions(m.questions)
	case "enter":
		answer := strings.TrimSpace(m.input.Value())
		respond := m.pendingQuestion.Respond
		m.pendingQuestion = nil
		m.input.Reset()
		m.input.Prompt = " › "
		m.transcript.addNotice("> " + answer)
		m.refreshTranscript()
		respond(answer)
		if m.questions != nil {
			return m, listenForQuestions(m.questions)
		}
		return m, nil
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
		if m.pendingApproval.Operation.IsIrreversible || m.pendingApproval.Operation.RequiresOneTimeApproval {
			return m, nil
		}
		decision = core.ApprovalAlways
	case "n", "esc":
		decision = core.ApprovalDenied
	default:
		return m, nil
	}

	respond := m.pendingApproval.Respond
	m.pendingApproval = nil
	cmd := tea.Batch(
		func() tea.Msg { respond(decision); return nil },
		listenForApprovals(m.approvals),
	)
	cmd = m.armMascotAnimation(cmd)
	return m, cmd
}

// paletteRow is one palette suggestion: completion is the input text tab fills in.
type paletteRow struct {
	completion string
	label      string
}

// listPaletteRows reports what the palette suggests while idle: commands for a bare "/word", or
// the endpoint's models for "/model prefix". None means the palette is closed.
func (m chatModel) listPaletteRows() []paletteRow {
	value := m.input.Value()
	if m.cancelTurn != nil || m.keyPromptProfile != "" || !strings.HasPrefix(value, "/") {
		return nil
	}
	var rows []paletteRow
	if prefix, isModelArgument := strings.CutPrefix(value, "/model "); isModelArgument {
		if strings.Contains(prefix, " ") {
			return nil
		}
		for _, name := range m.modelNames {
			if strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
				rows = append(rows, paletteRow{completion: "/model " + name, label: name})
			}
		}
	} else if prefix, isThemeArgument := strings.CutPrefix(value, "/theme "); isThemeArgument {
		if strings.Contains(prefix, " ") {
			return nil
		}
		for _, name := range themeNames {
			if strings.HasPrefix(name, strings.ToLower(prefix)) {
				rows = append(rows, paletteRow{completion: "/theme " + name, label: name})
			}
		}
	} else if prefix, isProviderArgument := strings.CutPrefix(value, "/provider "); isProviderArgument {
		if strings.Contains(prefix, " ") {
			return nil
		}
		for _, name := range m.listProviderNames() {
			if strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
				rows = append(rows, paletteRow{completion: "/provider " + name, label: name})
			}
		}
	} else if !strings.Contains(value, " ") {
		for _, command := range filterSlashCommands(m.commands, value[1:]) {
			rows = append(rows, paletteRow{
				completion: "/" + command.name + " ",
				label:      fmt.Sprintf("%-16s %s", command.usage, command.summary),
			})
		}
	}
	return rows[:min(len(rows), maxPaletteRows)]
}

func (m chatModel) submitPrompt() (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(m.input.Value())
	m.input.Reset()
	m.selection = textSelection{} // commands and new turns can reshape the transcript under it
	m.paletteIndex = 0
	m.fitViewport()
	if prompt == "" {
		return m, nil
	}
	if strings.HasPrefix(prompt, "/") {
		return m.runSlashCommand(prompt)
	}

	turnCtx, cancel := context.WithCancel(m.ctx)
	m.cancelTurn, m.isCancelRequested = cancel, false
	m.turnStartedAt, m.turnElapsed = m.clock.ReportCurrentTime(), 0
	m.activeTurnUsage = core.Usage{}
	turnNumber := m.transcript.startTurn(prompt)
	m.refreshTranscript()
	m.viewport.GotoBottom()

	runTurn := m.actions.RunTurn
	runTurnCmd := func() tea.Msg {
		return turnFinishedMsg{turnNumber: turnNumber, err: runTurn(turnCtx, prompt)}
	}
	cmd := m.armMascotAnimation(runTurnCmd)
	return m, cmd
}

func (m *chatModel) requestCancel() {
	m.cancelTurn()
	m.isCancelRequested = true
}

func (m chatModel) finishTurn(msg turnFinishedMsg) (tea.Model, tea.Cmd) {
	m.updateTurnElapsed(m.clock.ReportCurrentTime())
	m.cancelTurn()
	m.cancelTurn = nil
	hadPendingQuestion := m.pendingQuestion != nil
	m.pendingQuestion = nil
	m.input.Prompt = " › "
	m.fitViewport() // the palette may open for text typed during the turn

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
	if !m.isReducedMotion && (outcome == turnSucceeded || outcome == turnFailed) {
		m.settleFrames = settleFrameCount
	}
	m.transcript.finishTurn(msg.turnNumber, outcome, failure, m.turnElapsed)
	m.refreshTranscript()

	if m.hasInputEnded {
		return m, tea.Quit
	}
	if hadPendingQuestion {
		cmd := m.armMascotAnimation(listenForQuestions(m.questions))
		return m, cmd
	}
	cmd := m.armMascotAnimation(nil)
	return m, cmd
}

func (m *chatModel) resize(width, height int) {
	m.width, m.height = max(width, 1), height
	m.selection = textSelection{} // rows reflow, so the selected cells no longer mean the same text
	m.viewport.SetWidth(m.width)
	m.fitViewport()
	m.input.SetWidth(max(m.width-lipgloss.Width(m.input.Prompt)-1, 1))
	if m.memoryEditor != nil {
		m.memoryEditor.textarea.SetWidth(max(m.width-4, 1))
		m.memoryEditor.textarea.SetHeight(max(m.inlinePanelHeight()-3, 3))
	}
	if m.settingsPanel != nil {
		m.settingsPanel.search.SetWidth(max(m.width-12, 1))
		m.settingsPanel.editor.SetWidth(max(m.width-11, 1))
	}
}

// fitViewport gives the transcript whatever height the chrome and open controls leave.
func (m *chatModel) fitViewport() {
	isFollowingLatest := m.viewport.AtBottom()
	m.viewport.SetHeight(max(m.height-chromeHeight-len(m.listPaletteRows())-m.inlinePanelHeight(), 1))
	m.refreshTranscript()
	if isFollowingLatest {
		m.viewport.GotoBottom()
	}
}

func (m chatModel) inlinePanelHeight() int {
	if m.undoPicker == nil && m.memoryEditor == nil && m.settingsPanel == nil && m.commandPanel == nil {
		return 0
	}
	return min(max(m.height-chromeHeight-1, 1), 10)
}

// refreshTranscript re-renders the transcript, following new output unless the user scrolled up.
func (m *chatModel) refreshTranscript() {
	isFollowingLatest := m.viewport.AtBottom()
	content := renderTranscript(
		m.transcript,
		m.width,
		m.viewport.Height(),
		m.modelName,
		m.isDictationAvailable(),
		m.theme,
		m.replies,
		m.mascotFrame,
		m.buildActiveTurnMetrics(),
	)
	if m.pendingQuestion != nil {
		content += "\n\n" + wrapWithMargin(m.theme.assistantLabel.Render("travis asks ›")+" "+m.pendingQuestion.Text, m.width)
	}
	m.viewport.SetContent(content)
	if isFollowingLatest {
		m.viewport.GotoBottom()
	}
}

func (m *chatModel) applyTheme(theme terminalTheme) {
	m.theme = theme
	styles := m.input.Styles()
	styles.Focused.Prompt = theme.assistantLabel
	styles.Cursor.Color = theme.cursor
	m.input.SetStyles(styles)
}

func (m *chatModel) armMascotAnimation(cmd tea.Cmd) tea.Cmd {
	if !m.shouldTickMascot() {
		return cmd
	}
	interval := m.reportMascotTickInterval()
	if m.isMascotTickArmed && m.mascotTickInterval == interval {
		return cmd
	}
	m.mascotTickGeneration++
	m.isMascotTickArmed = true
	m.mascotTickInterval = interval
	return tea.Batch(cmd, scheduleMascotTick(m.clock, interval, m.mascotTickGeneration))
}

func (m *chatModel) updateTurnElapsed(at time.Time) {
	if m.cancelTurn == nil || m.turnStartedAt.IsZero() {
		return
	}
	m.turnElapsed = max(at.Sub(m.turnStartedAt), 0)
}
