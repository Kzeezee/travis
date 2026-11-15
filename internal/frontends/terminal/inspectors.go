package terminal

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// DoctorMode controls whether diagnostics may make bounded network requests.
type DoctorMode int

const (
	DoctorOffline DoctorMode = iota
	DoctorLive
)

// InspectionActions provide read-only operational state to slash commands.
type InspectionActions struct {
	ReadStatus       func(context.Context) (InspectionReport, error)
	ReadPermissions  func(context.Context) (InspectionReport, error)
	ReadInstructions func(context.Context) (InspectionReport, error)
	ReadContext      func(context.Context) (InspectionReport, error)
	RunDoctor        func(context.Context, DoctorMode) (InspectionReport, error)
	ReadSession      func(context.Context) (InspectionReport, error)
	RevokePermission func(context.Context, string) error
}

type inspectionFinishedMsg struct {
	name   string
	report InspectionReport
	err    error
}

type permissionRevokedMsg struct {
	id  string
	err error
}

func requestInspection(name string, read func(context.Context) (InspectionReport, error)) func(chatModel, string) (chatModel, tea.Cmd) {
	return func(m chatModel, args string) (chatModel, tea.Cmd) {
		if strings.TrimSpace(args) != "" {
			m.transcript.addNotice("usage: /" + name)
			return m, nil
		}
		if read == nil {
			m.transcript.addNotice("○ /" + name + " is unavailable in this session.")
			return m, nil
		}
		ctx := m.ctx
		return m, func() tea.Msg {
			report, err := read(ctx)
			return inspectionFinishedMsg{name: name, report: report, err: err}
		}
	}
}

func inspectStatus(m chatModel, args string) (chatModel, tea.Cmd) {
	return requestInspection("status", m.actions.Inspect.ReadStatus)(m, args)
}

func inspectInstructions(m chatModel, args string) (chatModel, tea.Cmd) {
	return requestInspection("instructions", m.actions.Inspect.ReadInstructions)(m, args)
}

func inspectContext(m chatModel, args string) (chatModel, tea.Cmd) {
	return requestInspection("context", m.actions.Inspect.ReadContext)(m, args)
}

func inspectSession(m chatModel, args string) (chatModel, tea.Cmd) {
	return requestInspection("session", m.actions.Inspect.ReadSession)(m, args)
}

func inspectPermissions(m chatModel, args string) (chatModel, tea.Cmd) {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		if m.actions.Inspect.ReadPermissions == nil {
			return m.openCommandPanel(commandPanel{
				kind: commandPanelPermissions, title: "permissions", lines: []string{"permissions are unavailable"},
			}), nil
		}
		read, ctx := m.actions.Inspect.ReadPermissions, m.ctx
		m = m.openCommandPanel(commandPanel{
			kind: commandPanelPermissions, title: "permissions", lines: []string{"loading permissions…"},
		})
		return m, func() tea.Msg {
			report, err := read(ctx)
			return inspectionFinishedMsg{name: "permissions", report: report, err: err}
		}
	}
	if len(fields) != 2 || fields[0] != "revoke" {
		m.transcript.addNotice("usage: /permissions [revoke <rule-id>]")
		return m, nil
	}
	if m.actions.Inspect.RevokePermission == nil {
		m.transcript.addNotice("○ Remembered permissions cannot be revoked in this session.")
		return m, nil
	}
	m.confirmation = &confirmation{
		title: "Revoke remembered permission " + fields[1] + "?",
		accept: func(ctx context.Context) tea.Msg {
			err := m.actions.Inspect.RevokePermission(ctx, fields[1])
			return permissionRevokedMsg{id: fields[1], err: err}
		},
	}
	return m, nil
}

func runDoctor(m chatModel, args string) (chatModel, tea.Cmd) {
	mode := DoctorOffline
	switch strings.TrimSpace(args) {
	case "":
	case "live":
		mode = DoctorLive
	default:
		m.transcript.addNotice("usage: /doctor [live]")
		return m, nil
	}
	if m.actions.Inspect.RunDoctor == nil {
		m.transcript.addNotice("○ Health checks are unavailable in this session.")
		return m, nil
	}
	run, ctx := m.actions.Inspect.RunDoctor, m.ctx
	return m, func() tea.Msg {
		report, err := run(ctx, mode)
		return inspectionFinishedMsg{name: "doctor", report: report, err: err}
	}
}

func (m chatModel) finishInspection(msg inspectionFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.name == "permissions" {
		if m.commandPanel == nil || m.commandPanel.kind != commandPanelPermissions {
			return m, nil
		}
		lines := []string(nil)
		if msg.err != nil {
			lines = []string{"✗ Could not load permissions", "  " + cutFirstLine(msg.err.Error())}
		} else {
			lines = buildReportLines(msg.report)
		}
		m = m.openCommandPanel(commandPanel{
			kind: commandPanelPermissions, title: "permissions", lines: lines,
		})
		return m, nil
	}
	if msg.err != nil {
		m.transcript.addNotice("✗ Could not run /" + msg.name)
		m.transcript.addNotice("  " + cutFirstLine(msg.err.Error()))
	} else {
		for _, line := range buildReportLines(msg.report) {
			m.transcript.addNotice(line)
		}
	}
	m.refreshTranscript()
	m.viewport.GotoBottom()
	return m, nil
}

func (m chatModel) finishPermissionRevocation(msg permissionRevokedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.transcript.addNotice("✗ Could not revoke remembered permission")
		m.transcript.addNotice("  " + cutFirstLine(msg.err.Error()))
	} else {
		m.transcript.addNotice("✓ Remembered permission revoked")
		m.transcript.addNotice("  Rule ID: " + msg.id)
	}
	m.refreshTranscript()
	m.viewport.GotoBottom()
	return m, nil
}
