package terminal

import tea "charm.land/bubbletea/v2"

// defaultPushToTalkKey is ctrl+t, not §5.10's ctrl+space: VS Code's terminal and macOS input
// switching both take ctrl+space before travis sees it.
const defaultPushToTalkKey = "ctrl+t"

// dictationState is where push-to-talk is: a recording passes idle → starting → recording →
// transcribing → idle. Voice only ever fills the input box; it never submits (§5.10, D-10).
type dictationState int

const (
	dictationIdle dictationState = iota
	dictationStarting
	dictationRecording
	dictationTranscribing
)

type dictationStartedMsg struct {
	finish func() (string, error)
	err    error
}

type dictationFinishedMsg struct {
	text        string
	err         error
	isDiscarded bool
}

func (m chatModel) isDictationAvailable() bool {
	return m.actions.StartDictation != nil
}

func (m chatModel) isModalOpen() bool {
	return m.pendingApproval != nil || m.pendingQuestion != nil || m.undoPicker != nil
}

// handlePushToTalkPress starts a recording, or stops one where the terminal cannot report the
// key's release and push-to-talk falls back to a toggle.
func (m chatModel) handlePushToTalkPress(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.IsRepeat {
		return m, nil
	}
	switch {
	case m.dictation == dictationIdle:
		m.dictation, m.pushToTalkCode, m.isDictationStopRequested = dictationStarting, key.Code, false
		start := m.actions.StartDictation
		ctx := m.ctx
		return m, func() tea.Msg {
			finish, err := start(ctx)
			return dictationStartedMsg{finish: finish, err: err}
		}
	case !m.canHoldToTalk:
		return m.stopDictation(false)
	}
	return m, nil
}

// handlePushToTalkRelease stops a held recording when the push-to-talk key comes back up.
func (m chatModel) handlePushToTalkRelease(key tea.KeyReleaseMsg) (tea.Model, tea.Cmd) {
	if !m.canHoldToTalk || key.Code != m.pushToTalkCode {
		return m, nil
	}
	return m.stopDictation(false)
}

func (m chatModel) handleDictationStarted(msg dictationStartedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.dictation = dictationIdle
		m.transcript.addNotice("✗ voice: " + cutFirstLine(msg.err.Error()))
		m.refreshTranscript()
		return m, nil
	}
	m.dictation, m.finishDictation = dictationRecording, msg.finish
	switch {
	case m.isModalOpen():
		return m.stopDictation(true)
	case m.isDictationStopRequested:
		return m.stopDictation(false)
	}
	return m, nil
}

// stopDictation ends the recording; discarded audio is still finished, so the microphone closes,
// but its transcript never reaches the input box.
func (m chatModel) stopDictation(isDiscarded bool) (tea.Model, tea.Cmd) {
	switch m.dictation {
	case dictationStarting:
		m.isDictationStopRequested = true
		return m, nil
	case dictationRecording:
		finish := m.finishDictation
		m.dictation, m.finishDictation = dictationTranscribing, nil
		return m, func() tea.Msg {
			text, err := finish()
			return dictationFinishedMsg{text: text, err: err, isDiscarded: isDiscarded}
		}
	case dictationIdle, dictationTranscribing:
	}
	return m, nil
}

// muteDictationForModal discards any recording when a modal opens: the microphone never
// listens while an approval or question is on screen (§5.10, D-10).
func (m chatModel) muteDictationForModal() (chatModel, tea.Cmd) {
	model, cmd := m.stopDictation(true)
	return model.(chatModel), cmd
}

// handleDictationFinished puts the transcript at the end of the input for the user to edit and
// send. A transcript that lands while a modal is open is dropped, never typed into the modal.
func (m chatModel) handleDictationFinished(msg dictationFinishedMsg) (tea.Model, tea.Cmd) {
	m.dictation = dictationIdle
	switch {
	case msg.isDiscarded || m.isModalOpen():
	case msg.err != nil:
		m.transcript.addNotice("✗ voice: " + cutFirstLine(msg.err.Error()))
		m.refreshTranscript()
	case msg.text != "":
		value := m.input.Value()
		if value != "" && value[len(value)-1] != ' ' {
			value += " "
		}
		m.input.SetValue(value + msg.text)
		m.input.CursorEnd()
		m.fitViewport()
	}
	return m, nil
}

func (m chatModel) reportDictationStatus() string {
	if !m.isDictationAvailable() {
		return ""
	}
	switch m.dictation {
	case dictationStarting, dictationRecording:
		return " · ● recording"
	case dictationTranscribing:
		return " · transcribing…"
	case dictationIdle:
	}
	if !m.canHoldToTalk {
		return " · " + m.pushToTalkKey + ": dictate (toggle, terminal)"
	}
	return " · hold " + m.pushToTalkKey + ": dictate"
}
