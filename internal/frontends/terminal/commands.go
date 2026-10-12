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
		{name: "quit", usage: "/quit", summary: "exit travis", run: func(m chatModel, _ string) (chatModel, tea.Cmd) {
			return m, tea.Quit
		}},
		{name: "new", usage: "/new", summary: "start a new conversation", run: startNewConversation},
		{name: "model", usage: "/model [name]", summary: "show or switch the model", run: selectModel},
		{name: "tools", usage: "/tools", summary: "list available tools", run: showTools},
		{name: "cost", usage: "/cost", summary: "show tokens used this session", run: showCost},
		{name: "undo", usage: "/undo", summary: "rewind to before an earlier turn", run: func(m chatModel, _ string) (chatModel, tea.Cmd) {
			return m.requestTurnList()
		}},
	}
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
	for _, command := range m.commands {
		m.transcript.addNotice(command.usage + " — " + command.summary)
	}
	return m, nil
}

func startNewConversation(m chatModel, _ string) (chatModel, tea.Cmd) {
	m.actions.ResetHistory()
	m.transcript.resetTranscript()
	m.lastTurnOutcome = turnPending
	m.transcript.addNotice("new conversation")
	return m, nil
}

func selectModel(m chatModel, name string) (chatModel, tea.Cmd) {
	if name == "" {
		m.transcript.addNotice("model: " + m.modelName)
		return m, nil
	}
	m.actions.SelectModel(name)
	m.modelName = name
	m.transcript.addNotice("model → " + name)
	return m, nil
}

func showTools(m chatModel, _ string) (chatModel, tea.Cmd) {
	if len(m.actions.ToolNames) == 0 {
		m.transcript.addNotice("tools: none")
		return m, nil
	}
	m.transcript.addNotice("tools: " + strings.Join(m.actions.ToolNames, ", "))
	return m, nil
}

func showCost(m chatModel, _ string) (chatModel, tea.Cmd) {
	usage := m.transcript.usage
	m.transcript.addNotice(fmt.Sprintf("tokens: in %d · out %d", usage.InputTokens, usage.OutputTokens))
	return m, nil
}

// runSlashCommand dispatches "/word args"; an unknown word becomes a notice and never reaches the model.
func (m chatModel) runSlashCommand(input string) (tea.Model, tea.Cmd) {
	word, args, _ := strings.Cut(input[1:], " ")
	command, isFound := findSlashCommand(m.commands, word)
	var cmd tea.Cmd
	if isFound {
		m, cmd = command.run(m, strings.TrimSpace(args))
	} else {
		m.transcript.addNotice("unknown command /" + word + " — try /help")
	}
	m.refreshTranscript()
	m.viewport.GotoBottom()
	return m, cmd
}
