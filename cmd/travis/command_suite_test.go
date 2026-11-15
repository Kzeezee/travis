package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/tools/mcp"
)

type inspectingMemoryStore struct{}

func (inspectingMemoryStore) ReadMemory(context.Context) (string, error)  { return "", nil }
func (inspectingMemoryStore) ReplaceMemory(context.Context, string) error { return nil }
func (inspectingMemoryStore) ReportSecuritySnapshot(context.Context) (broker.SecuritySnapshot, error) {
	return broker.SecuritySnapshot{}, nil
}

type countingListingProvider struct {
	calls int
}

func (*countingListingProvider) Chat(context.Context, core.ProviderRequest) (core.Stream, error) {
	panic("doctor must not generate")
}

func (p *countingListingProvider) ListModels(context.Context) ([]string, error) {
	p.calls++
	return []string{"model"}, nil
}

func TestDoctorOfflineDoesNotProbeTheProvider(t *testing.T) {
	root := t.TempDir()
	paths := config.PathSet{WorkspaceDir: root, StateDir: filepath.Join(root, "state"), ConfigDir: filepath.Join(root, "config")}
	if err := os.MkdirAll(buildSessionsDir(paths), 0o700); err != nil {
		t.Fatal(err)
	}
	provider := &countingListingProvider{}
	switcher := &providerSwitcher{current: provider, active: "local", profiles: map[string]config.ProviderProfileCfg{
		"local": {BaseURL: "http://127.0.0.1", Model: "model"},
	}}
	suite := &commandSuite{
		cfg:   config.Config{Security: config.SecurityCfg{Profile: "default"}},
		paths: paths, sessionID: "session", switcher: switcher,
		session: &chatSession{memoryStore: inspectingMemoryStore{}, mcpManager: &mcp.Manager{}},
	}
	report, err := suite.runDoctor(context.Background(), terminal.DoctorOffline)
	if err != nil || len(report.Sections) == 0 || len(report.Sections[0].Rows) == 0 {
		t.Fatalf("runDoctor = %+v, %v", report, err)
	}
	if provider.calls != 0 {
		t.Fatalf("offline doctor made %d provider probes", provider.calls)
	}
}

func TestDoctorLiveMakesOneBoundedActiveProviderProbe(t *testing.T) {
	root := t.TempDir()
	paths := config.PathSet{WorkspaceDir: root, StateDir: filepath.Join(root, "state"), ConfigDir: filepath.Join(root, "config")}
	if err := os.MkdirAll(buildSessionsDir(paths), 0o700); err != nil {
		t.Fatal(err)
	}
	provider := &countingListingProvider{}
	switcher := &providerSwitcher{current: provider, active: "local", profiles: map[string]config.ProviderProfileCfg{
		"local": {BaseURL: "http://127.0.0.1", Model: "model"},
	}}
	suite := &commandSuite{
		cfg:   config.Config{Security: config.SecurityCfg{Profile: "default"}},
		paths: paths, sessionID: "session", switcher: switcher,
		session: &chatSession{memoryStore: inspectingMemoryStore{}, mcpManager: &mcp.Manager{}},
	}
	if _, err := suite.runDoctor(context.Background(), terminal.DoctorLive); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 {
		t.Fatalf("live doctor made %d active provider probes, want one", provider.calls)
	}
}

func TestDoctorReportUsesPlainLanguageStatusesAndTechnicalDetails(t *testing.T) {
	root := t.TempDir()
	paths := config.PathSet{WorkspaceDir: root, StateDir: filepath.Join(root, "state"), ConfigDir: filepath.Join(root, "config")}
	if err := os.MkdirAll(buildSessionsDir(paths), 0o700); err != nil {
		t.Fatal(err)
	}
	suite := &commandSuite{
		cfg: config.Config{Security: config.SecurityCfg{Profile: "default"}}, paths: paths, sessionID: "session",
		switcher: &providerSwitcher{current: &countingListingProvider{}, active: "local", profiles: map[string]config.ProviderProfileCfg{
			"local": {BaseURL: "http://127.0.0.1", Model: "model"},
		}},
		session: &chatSession{memoryStore: inspectingMemoryStore{}, mcpManager: &mcp.Manager{}},
	}

	report, err := suite.runDoctor(context.Background(), terminal.DoctorOffline)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary != "Travis health check" || !reportHasLabel(report, "Configuration") ||
		!reportHasRow(report, "Provider connection", "Not checked", terminal.ReportNeutral) {
		t.Fatalf("doctor report = %+v", report)
	}
	for _, section := range report.Sections {
		for _, row := range section.Rows {
			if strings.HasPrefix(row.Value, "OK ") || strings.HasPrefix(row.Value, "FAIL ") {
				t.Fatalf("doctor row leaks machine-oriented status: %+v", row)
			}
		}
	}
}

func TestPermissionAndSessionReportsExplainTheirMeaning(t *testing.T) {
	root := t.TempDir()
	paths := config.PathSet{WorkspaceDir: root, StateDir: filepath.Join(root, "state"), ConfigDir: filepath.Join(root, "config")}
	if err := os.MkdirAll(buildSessionsDir(paths), 0o700); err != nil {
		t.Fatal(err)
	}
	suite := &commandSuite{
		cfg: config.Config{Security: config.SecurityCfg{Profile: "default"}}, paths: paths, sessionID: "session",
		session: &chatSession{memoryStore: inspectingMemoryStore{}, mcpManager: &mcp.Manager{}},
	}

	permissions, err := suite.readPermissions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if permissions.Summary != "What Travis can access in this session" ||
		!reportHasRow(permissions, "Remembered permissions", "None", terminal.ReportUnavailable) {
		t.Fatalf("permissions report = %+v", permissions)
	}

	sessions, err := suite.readSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sessions.Summary != "Current and recent conversations" ||
		!reportHasRow(sessions, "Status", "Active", terminal.ReportCurrent) {
		t.Fatalf("session report = %+v", sessions)
	}
}

func reportHasLabel(report terminal.InspectionReport, label string) bool {
	for _, section := range report.Sections {
		for _, row := range section.Rows {
			if row.Label == label {
				return true
			}
		}
	}
	return false
}

func reportHasRow(report terminal.InspectionReport, label, value string, status terminal.ReportStatus) bool {
	for _, section := range report.Sections {
		for _, row := range section.Rows {
			if row.Label == label && row.Value == value && row.Status == status {
				return true
			}
		}
	}
	return false
}

func TestAbbreviateIdentifierKeepsBothEnds(t *testing.T) {
	if got := abbreviateIdentifier("1234567890abcdefghijklmnopqrstuvwxyz"); got != "12345678…stuvwxyz" {
		t.Fatalf("abbreviated identifier = %q", got)
	}
	if got := abbreviateIdentifier("short-id"); got != "short-id" {
		t.Fatalf("short identifier changed to %q", got)
	}
}
