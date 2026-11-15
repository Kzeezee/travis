package terminal

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/Kzeezee/travis/internal/core"
)

func TestSettingsPanelSearchesAcrossTypedRows(t *testing.T) {
	search := textinput.New()
	search.SetValue("voice")
	panel := settingsPanel{
		search: search,
		snapshot: SettingsSnapshot{Rows: []SettingRow{
			{Key: "theme", Label: "Theme", Value: "dark", Scope: "saved"},
			{Key: "voice_input", Label: "Voice input", Value: "off", Scope: "saved"},
		}},
	}
	rows := panel.filteredRows()
	if len(rows) != 1 || rows[0].Key != "voice_input" {
		t.Fatalf("filtered rows = %+v, want voice input only", rows)
	}
}

func TestMemoryAddUsesRevisionSafeChangeAction(t *testing.T) {
	var received MemoryChange
	actions := ChatActions{Memory: MemoryActions{
		Read: func(context.Context) (MemoryDocument, error) { return MemoryDocument{}, nil },
		Change: func(_ context.Context, change MemoryChange) (MemoryDocument, error) {
			received = change
			return MemoryDocument{Trusted: change.Content, Revision: "next"}, nil
		},
	}}
	model := buildChatModel(context.Background(), make(chan core.Event), nil, actions, Options{})
	_, command := manageMemory(model, "add remember this")
	if command == nil {
		t.Fatal("add returned no command")
	}
	message := command()
	if _, ok := message.(memoryChangedMsg); !ok {
		t.Fatalf("message = %T, want memoryChangedMsg", message)
	}
	if received.Kind != MemoryAppend || received.Content != "remember this" {
		t.Fatalf("change = %+v", received)
	}
}

func TestMemoryPanelKeepsOnlyEditAndClearWhileAddRequiresText(t *testing.T) {
	actions := ChatActions{Memory: MemoryActions{
		Read:   func(context.Context) (MemoryDocument, error) { return MemoryDocument{}, nil },
		Change: func(context.Context, MemoryChange) (MemoryDocument, error) { return MemoryDocument{}, nil },
	}}
	model := buildChatModel(context.Background(), make(chan core.Event), nil, actions, Options{})

	panelModel, _ := manageMemory(model, "")
	if panelModel.commandPanel == nil || len(panelModel.commandPanel.options) != 2 ||
		panelModel.commandPanel.options[0].value != "edit" || panelModel.commandPanel.options[1].value != "clear" {
		t.Fatalf("memory panel = %+v, want edit and clear only", panelModel.commandPanel)
	}

	withoutText, command := manageMemory(model, "add")
	if command != nil || !strings.Contains(strings.Join(withoutText.transcript.notices, "\n"), "add <text>") {
		t.Fatal("/memory add without text did not show usage")
	}
	legacyShow, command := manageMemory(model, "show")
	if command != nil || !strings.Contains(strings.Join(legacyShow.transcript.notices, "\n"), "add <text>") {
		t.Fatal("legacy /memory show was not rejected with current usage")
	}
}

func TestDoctorLiveIsExplicitAndPermissionsRevokeConfirms(t *testing.T) {
	var mode DoctorMode
	var revoked string
	actions := ChatActions{Inspect: InspectionActions{
		RunDoctor: func(_ context.Context, requested DoctorMode) (InspectionReport, error) {
			mode = requested
			return InspectionReport{}, nil
		},
		RevokePermission: func(_ context.Context, id string) error {
			revoked = id
			return nil
		},
	}}
	model := buildChatModel(context.Background(), make(chan core.Event), nil, actions, Options{})
	_, command := runDoctor(model, "live")
	_ = command()
	if mode != DoctorLive {
		t.Fatalf("doctor mode = %v, want live", mode)
	}
	updated, _ := inspectPermissions(model, "revoke rule-1")
	if updated.confirmation == nil || revoked != "" {
		t.Fatal("revoke ran without confirmation")
	}
	_, command = updated.handleConfirmationKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if command == nil {
		t.Fatal("confirmation returned no revoke command")
	}
	_ = command()
	if revoked != "rule-1" {
		t.Fatalf("revoked %q, want rule-1", revoked)
	}
}
