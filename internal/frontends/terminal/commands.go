package terminal

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// slashCommand is one chat command; run gets the trimmed text after the command word.
type slashCommand struct {
	name, usage, summary string
	run                  func(m chatModel, args string) (chatModel, tea.Cmd)
}

// buildSlashCommands lists the chat commands; /undo reports for itself when the session cannot undo.
func buildSlashCommands() []slashCommand {
	return []slashCommand{
		{name: "help", usage: "/help", summary: "list commands", run: showHelp},
		{name: "quit", usage: "/quit", summary: "exit travis", run: quitChat},
		{name: "exit", usage: "/exit", summary: "exit travis", run: quitChat},
		{name: "new", usage: "/new", summary: "start a new conversation", run: startNewConversation},
		{name: "status", usage: "/status", summary: "show current configuration", run: inspectStatus},
		{name: "settings", usage: "/settings", summary: "change saved preferences", run: openSettings},
		{name: "permissions", usage: "/permissions [revoke <rule-id>]", summary: "review access and remembered permissions", run: inspectPermissions},
		{name: "instructions", usage: "/instructions", summary: "show instructions Travis can use", run: inspectInstructions},
		{name: "context", usage: "/context", summary: "show what the latest model request contained", run: inspectContext},
		{name: "doctor", usage: "/doctor [live]", summary: "check Travis health", run: runDoctor},
		{name: "session", usage: "/session", summary: "show current and recent conversations", run: inspectSession},
		{name: "model", usage: "/model [name]", summary: "view or change the model", run: selectModel},
		{name: "theme", usage: "/theme [name]", summary: "change terminal colours", run: selectTheme},
		{name: "voice", usage: voiceCommandUsage, summary: "change voice controls", run: configureVoice},
		{name: "provider", usage: "/provider [name]", summary: "view or change the AI provider", run: func(m chatModel, name string) (chatModel, tea.Cmd) {
			return m.requestProviderSwitch(name)
		}},
		{name: "mcp", usage: "/mcp", summary: "show external tool servers", run: showMCPServers},
		{name: "tools", usage: "/tools", summary: "show available tools", run: showTools},
		{name: "skills", usage: "/skills", summary: "show loaded skills", run: showSkills},
		{name: "memory", usage: "/memory [add <text>|edit|clear]", summary: "edit or clear saved memory", run: manageMemory},
		{name: "cost", usage: "/cost", summary: "show session token use and cost", run: showCost},
		{name: "undo", usage: "/undo", summary: "rewind an earlier turn", run: func(m chatModel, _ string) (chatModel, tea.Cmd) {
			return m.requestTurnList()
		}},
	}
}

func quitChat(m chatModel, _ string) (chatModel, tea.Cmd) {
	return m, tea.Quit
}

func selectTheme(m chatModel, name string) (chatModel, tea.Cmd) {
	if name == "" {
		options := make([]commandPanelOption, 0, len(themeNames))
		for _, candidate := range themeNames {
			detail := ""
			if candidate == m.themePreference {
				detail = "current"
			}
			status := ReportNeutral
			if candidate == m.themePreference {
				status = ReportCurrent
			}
			options = append(options, commandPanelOption{label: candidate, detail: detail, value: candidate, status: status})
		}
		return m.openCommandPanel(commandPanel{kind: commandPanelTheme, title: "theme", options: options}), nil
	}
	isKnown := false
	for _, candidate := range themeNames {
		if name == candidate {
			isKnown = true
			break
		}
	}
	if !isKnown {
		m.transcript.addNotice("! Unknown theme: " + name)
		m.transcript.addNotice("  Available themes: " + strings.Join(themeNames, ", "))
		return m, nil
	}
	m.themePreference = name
	resolved := name
	if name == themeAuto {
		resolved = themeDark
		if !m.isDarkBackground {
			resolved = themeLight
		}
	}
	m.applyTheme(buildTerminalTheme(resolved))
	m.transcript.addNotice("✓ Theme changed for this session: " + name)
	if name == themeAuto {
		return m, tea.RequestBackgroundColor
	}
	return m, nil
}

// findSlashCommand matches a command by name.
func findSlashCommand(commands []slashCommand, word string) (slashCommand, bool) {
	for _, command := range commands {
		if word == command.name {
			return command, true
		}
	}
	return slashCommand{}, false
}

func filterSlashCommands(commands []slashCommand, prefix string) []slashCommand {
	var matches []slashCommand
	for _, command := range commands {
		if strings.HasPrefix(command.name, prefix) {
			matches = append(matches, command)
		}
	}
	return matches
}

func showHelp(m chatModel, _ string) (chatModel, tea.Cmd) {
	options := make([]commandPanelOption, 0, len(m.commands))
	categories := []string{"Conversation", "Configure", "Inspect", "Integrations", "Memory and history", "Help", "Exit"}
	for _, category := range categories {
		for _, command := range m.commands {
			if reportCommandCategory(command.name) == category {
				options = append(options, commandPanelOption{
					section: category, label: command.usage, detail: command.summary,
				})
			}
		}
	}
	return m.openCommandPanel(commandPanel{kind: commandPanelHelp, title: "help", options: options}), nil
}

func startNewConversation(m chatModel, _ string) (chatModel, tea.Cmd) {
	m.actions.ResetHistory()
	m.transcript.resetTranscript()
	m.lastTurnOutcome = turnPending
	m.contextTokens = 0
	m.turnCostUSD = 0
	m.transcript.addNotice("> /new")
	m.transcript.addNotice("✓ Started a new conversation")
	return m, nil
}

func selectModel(m chatModel, name string) (chatModel, tea.Cmd) {
	if name == "" {
		if len(m.modelNames) == 0 {
			return m.openCommandPanel(commandPanel{
				kind: commandPanelModel, title: "model",
				lines: []string{"current: " + m.modelName, "no model list is available from this provider"},
			}), nil
		}
		options := make([]commandPanelOption, 0, len(m.modelNames))
		for _, candidate := range m.modelNames {
			detail := ""
			if candidate == m.modelName {
				detail = "current"
			}
			status := ReportNeutral
			if candidate == m.modelName {
				status = ReportCurrent
			}
			options = append(options, commandPanelOption{label: candidate, detail: detail, value: candidate, status: status})
		}
		return m.openCommandPanel(commandPanel{kind: commandPanelModel, title: "model", options: options}), nil
	}
	m.actions.SelectModel(name)
	m.modelName = name
	m.transcript.addNotice("✓ Model changed for this session: " + name)
	return m, nil
}

func reportCommandCategory(name string) string {
	switch name {
	case "new":
		return "Conversation"
	case "settings", "theme", "model", "provider", "voice":
		return "Configure"
	case "status", "permissions", "instructions", "context", "doctor", "session", "cost":
		return "Inspect"
	case "mcp", "tools", "skills":
		return "Integrations"
	case "memory", "undo":
		return "Memory and history"
	case "help":
		return "Help"
	case "quit", "exit":
		return "Exit"
	default:
		return "Other"
	}
}

func showTools(m chatModel, _ string) (chatModel, tea.Cmd) {
	if m.actions.ListTools == nil {
		m.appendReport(InspectionReport{Summary: "No tools are available in this session."})
		return m, nil
	}
	names := m.actions.ListTools()
	if len(names) == 0 {
		m.appendReport(InspectionReport{Summary: "No tools are available in this session."})
		return m, nil
	}
	rows := make([]ReportRow, 0, len(names))
	for _, name := range names {
		rows = append(rows, ReportRow{Label: name, Value: "Available", Status: ReportSuccess})
	}
	m.appendReport(InspectionReport{
		Summary:  reportCount(len(names), "tool") + " available",
		Sections: []ReportSection{{Title: "Tools", Rows: rows}},
	})
	return m, nil
}

// MCPServerSummary is one configured MCP server as /mcp lists it: plain values only.
type MCPServerSummary struct {
	Name, State, Reason string
	ToolCount           int
	HiddenToolCount     int
}

func showMCPServers(m chatModel, _ string) (chatModel, tea.Cmd) {
	var servers []MCPServerSummary
	if m.actions.ListMCPServers != nil {
		servers = m.actions.ListMCPServers()
	}
	if len(servers) == 0 {
		m.appendReport(InspectionReport{
			Summary: "No external tool servers are configured.",
			Details: []ReportRow{{
				Label: "To add one", Value: "Open Settings or edit the tool-server section of your user configuration.",
			}},
		})
		return m, nil
	}
	rows := make([]ReportRow, 0, len(servers))
	var details []ReportRow
	for _, server := range servers {
		value := fmt.Sprintf("%s · %d tools", describeMCPState(server.State), server.ToolCount)
		if server.HiddenToolCount > 0 {
			value += fmt.Sprintf(" · %d unavailable", server.HiddenToolCount)
		}
		status := ReportNeutral
		switch strings.ToLower(server.State) {
		case "running", "ready":
			status = ReportSuccess
		case "starting":
			status = ReportLoading
		case "failed", "failed to start", "unavailable":
			status = ReportError
		}
		rows = append(rows, ReportRow{Label: server.Name, Value: value, Status: status})
		if server.Reason != "" {
			details = append(details, ReportRow{Label: server.Name + " details", Value: cutFirstLine(server.Reason)})
		}
	}
	m.appendReport(InspectionReport{
		Summary:  reportCount(len(servers), "external tool server") + " configured",
		Sections: []ReportSection{{Title: "Servers", Rows: rows}},
		Details:  details,
	})
	return m, nil
}

func describeMCPState(state string) string {
	switch strings.ToLower(state) {
	case "running", "ready":
		return "Ready"
	case "starting":
		return "Starting"
	case "failed", "failed to start", "unavailable":
		return "Unavailable"
	default:
		return state
	}
}

func showSkills(m chatModel, _ string) (chatModel, tea.Cmd) {
	if m.actions.ListSkills == nil {
		m.appendReport(InspectionReport{Summary: "No skills are loaded."})
		return m, nil
	}
	skills := m.actions.ListSkills()
	if len(skills) == 0 {
		m.appendReport(InspectionReport{Summary: "No skills are loaded."})
		return m, nil
	}
	rows := make([]ReportRow, 0, len(skills))
	for _, skill := range skills {
		rows = append(rows, ReportRow{
			Label: skill.Name, Value: skill.Description, Detail: describeSkillTier(skill.Tier), Status: ReportSuccess,
		})
	}
	m.appendReport(InspectionReport{
		Summary:  reportCount(len(skills), "skill") + " ready to use",
		Sections: []ReportSection{{Title: "Skills", Rows: rows}},
	})
	return m, nil
}

func reportCount(count int, singular string) string {
	if count == 1 {
		return "1 " + singular + " is"
	}
	return fmt.Sprintf("%d %ss are", count, singular)
}

func describeSkillTier(tier string) string {
	switch tier {
	case "workspace":
		return "Provided by this project."
	case "user":
		return "Installed for your user account."
	default:
		return "Source: " + tier
	}
}

func showCost(m chatModel, _ string) (chatModel, tea.Cmd) {
	usage := m.transcript.usage
	rows := []ReportRow{
		{Label: "Input tokens", Value: fmt.Sprint(usage.InputTokens)},
		{Label: "Output tokens", Value: fmt.Sprint(usage.OutputTokens)},
		{Label: "Total tokens", Value: fmt.Sprint(usage.InputTokens + usage.OutputTokens)},
	}
	if m.isPriced {
		rows = append(rows, ReportRow{Label: "Estimated cost", Value: fmt.Sprintf("$%.2f", m.transcript.costUSD)})
	}
	m.appendReport(InspectionReport{
		Summary:  "Usage for this session",
		Sections: []ReportSection{{Title: "Tokens and cost", Rows: rows}},
	})
	return m, nil
}

// runSlashCommand dispatches "/word args"; an unknown word becomes a notice and never reaches the model.
func (m chatModel) runSlashCommand(input string) (tea.Model, tea.Cmd) {
	word, args, _ := strings.Cut(input[1:], " ")
	command, isFound := findSlashCommand(m.commands, word)
	var cmd tea.Cmd
	if !isFound || shouldRecordSlashCommand(word, strings.TrimSpace(args)) {
		m.transcript.addNotice("> " + input)
	}
	if isFound {
		m, cmd = command.run(m, strings.TrimSpace(args))
	} else {
		m.transcript.addNotice("! Unknown command: /" + word)
		m.transcript.addNotice("  Run /help to see available commands.")
	}
	m.refreshTranscript()
	m.viewport.GotoBottom()
	return m, cmd
}

func shouldRecordSlashCommand(name, args string) bool {
	switch name {
	case "help", "undo":
		return false
	case "settings":
		return false
	case "theme", "model", "provider":
		return args != ""
	case "permissions":
		return args != ""
	case "memory":
		action, _, _ := strings.Cut(args, " ")
		switch action {
		case "":
			return false
		case "edit", "clear":
			return false
		case "add":
			return true
		default:
			return true
		}
	default:
		return true
	}
}
