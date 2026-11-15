package terminal

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

const voiceCommandUsage = "/voice [status|input on|off|output on|off|key <binding>]"

type voiceConfiguredMsg struct {
	change VoiceChange
	state  VoiceConfiguration
	err    error
}

func configureVoice(m chatModel, args string) (chatModel, tea.Cmd) {
	fields := strings.Fields(args)
	if len(fields) == 0 || (len(fields) == 1 && fields[0] == "status") {
		m.appendReport(InspectionReport{
			Summary: "Voice controls",
			Sections: []ReportSection{{Title: "Current settings", Rows: []ReportRow{
				{Label: "Voice input", Value: reportEnabledDisabled(m.isVoiceInputEnabled)},
				{Label: "Voice output", Value: reportEnabledDisabled(m.isVoiceOutputEnabled)},
				{Label: "Push-to-talk key", Value: m.pushToTalkKey},
			}}},
		})
		return m, nil
	}
	if m.actions.ConfigureVoice == nil {
		m.transcript.addNotice("○ Voice settings are unavailable in this session.")
		return m, nil
	}
	if m.isVoiceConfiguring {
		m.transcript.addNotice("… Voice settings are already being updated.")
		return m, nil
	}

	change, isValid := parseVoiceChange(fields)
	if !isValid {
		m.transcript.addNotice("usage: " + voiceCommandUsage)
		return m, nil
	}
	m.isVoiceConfiguring = true
	configure := m.actions.ConfigureVoice
	return m, func() tea.Msg {
		state, err := configure(change)
		return voiceConfiguredMsg{change: change, state: state, err: err}
	}
}

func parseVoiceChange(fields []string) (VoiceChange, bool) {
	if len(fields) != 2 {
		return VoiceChange{}, false
	}
	switch fields[0] {
	case "input":
		isEnabled, ok := parseOnOff(fields[1])
		return VoiceChange{InputEnabled: &isEnabled}, ok
	case "output":
		isEnabled, ok := parseOnOff(fields[1])
		return VoiceChange{OutputEnabled: &isEnabled}, ok
	case "key":
		key := strings.TrimSpace(fields[1])
		return VoiceChange{PushToTalkKey: &key}, key != ""
	default:
		return VoiceChange{}, false
	}
}

func parseOnOff(value string) (bool, bool) {
	switch value {
	case "on":
		return true, true
	case "off":
		return false, true
	default:
		return false, false
	}
}

func (m chatModel) finishVoiceConfiguration(msg voiceConfiguredMsg) (tea.Model, tea.Cmd) {
	m.isVoiceConfiguring = false
	var cmd tea.Cmd
	if msg.err != nil {
		m.transcript.addNotice("✗ Could not update voice settings")
		m.transcript.addNotice("  " + cutFirstLine(msg.err.Error()))
	} else {
		if msg.change.InputEnabled != nil && !msg.state.IsInputEnabled {
			var model tea.Model
			model, cmd = m.stopDictation(true)
			m = model.(chatModel)
		}
		m.isVoiceInputEnabled = msg.state.IsInputEnabled
		m.isVoiceOutputEnabled = msg.state.IsOutputEnabled
		m.pushToTalkKey = msg.state.PushToTalkKey
		m.actions.StartDictation = msg.state.StartDictation
		switch {
		case msg.change.InputEnabled != nil:
			m.transcript.addNotice("✓ Voice input: " + reportEnabledDisabled(msg.state.IsInputEnabled))
		case msg.change.OutputEnabled != nil:
			m.transcript.addNotice("✓ Voice output: " + reportEnabledDisabled(msg.state.IsOutputEnabled))
		case msg.change.PushToTalkKey != nil:
			m.transcript.addNotice("✓ Push-to-talk key: " + msg.state.PushToTalkKey)
		}
	}
	m.refreshTranscript()
	m.viewport.GotoBottom()
	return m, cmd
}

func reportEnabledDisabled(value bool) string {
	if value {
		return "Enabled"
	}
	return "Disabled"
}
