package terminal

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	"github.com/charmbracelet/x/exp/teatest/v2"
)

// Regenerate with: go test ./internal/frontends/terminal -run TestCommandOutputGoldenFrames -update
func TestCommandOutputGoldenFrames(t *testing.T) {
	tests := map[string]func(*testing.T) chatModel{
		"help": func(t *testing.T) chatModel {
			m := buildTestModel(ChatActions{})
			m, _ = pressKeys(m, append(typedKeys("/help"), enterKey)...)
			return m
		},
		"settings": func(t *testing.T) chatModel {
			m := buildTestModel(ChatActions{})
			search := textinput.New()
			search.Prompt = " search › "
			m.settingsPanel = &settingsPanel{search: search, snapshot: SettingsSnapshot{Rows: []SettingRow{
				{Label: "Theme", Value: "dark", Scope: "saved", Kind: SettingChoice},
				{Label: "Voice input", Value: "off", Scope: "saved", Kind: SettingToggle},
			}}}
			m.fitViewport()
			return m
		},
		"provider": func(t *testing.T) chatModel {
			m := buildTestModel(ChatActions{})
			return m.openCommandPanel(commandPanel{kind: commandPanelProvider, title: "provider", options: []commandPanelOption{
				{label: "local", detail: "qwen · Local · no key needed", value: "local", status: ReportCurrent},
				{label: "cloud", detail: "gpt · Needs an API key", value: "cloud", status: ReportWarning},
			}})
		},
		"permissions": func(t *testing.T) chatModel {
			m := buildTestModel(ChatActions{})
			report := InspectionReport{
				Summary: "What Travis can access in this session",
				Sections: []ReportSection{{Title: "Effective access", Rows: []ReportRow{
					{Label: "Security profile", Value: "standard", Status: ReportCurrent},
					{Label: "Allowed", Value: "read files", Status: ReportSuccess},
				}}},
			}
			return m.openCommandPanel(commandPanel{
				kind: commandPanelPermissions, title: "permissions", lines: buildReportLines(report),
			})
		},
		"memory": func(t *testing.T) chatModel {
			m := buildTestModel(ChatActions{})
			return m.openCommandPanel(commandPanel{kind: commandPanelMemory, title: "memory", options: []commandPanelOption{
				{section: "Saved memory", label: "Edit", detail: "Edit your trusted notes", value: "edit"},
				{section: "Saved memory", label: "Clear", detail: "Remove your trusted notes", value: "clear", status: ReportWarning},
			}})
		},
		"confirmation": func(t *testing.T) chatModel {
			m := buildTestModel(ChatActions{})
			m.confirmation = &confirmation{title: "Clear trusted memory? Generated notes are preserved."}
			return m
		},
		"approval": func(t *testing.T) chatModel {
			m, _ := buildPendingApproval(t)
			return m
		},
		"provider_key": func(t *testing.T) chatModel {
			m := buildTestModel(ChatActions{})
			m.keyPromptProfile = "cloud"
			m.input.EchoMode = textinput.EchoPassword
			m.input.Prompt = " key › "
			m.input.SetValue("secret-never-rendered")
			return m
		},
	}

	for _, name := range []string{"help", "settings", "provider", "permissions", "memory", "confirmation", "approval", "provider_key"} {
		t.Run(fmt.Sprintf("%s_width_50", name), func(t *testing.T) {
			m := tests[name](t)
			m.resize(50, 16)
			output := renderPlainView(m)
			if strings.Contains(output, "secret-never-rendered") {
				t.Fatal("provider key leaked into the golden frame")
			}
			teatest.RequireEqualOutput(t, []byte(trimRightPadding(output)))
		})
	}
}

func trimRightPadding(content string) string {
	lines := strings.Split(content, "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " ")
	}
	return strings.Join(lines, "\n")
}
