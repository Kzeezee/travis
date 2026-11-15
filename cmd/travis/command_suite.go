package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Kzeezee/travis/internal/agent/loop"
	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
	"github.com/Kzeezee/travis/internal/frontends/voicein"
	"github.com/Kzeezee/travis/internal/frontends/voiceout"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/security/sandbox"
)

type commandSuite struct {
	mu sync.Mutex

	cfg          config.Config
	paths        config.PathSet
	session      *chatSession
	sessionID    core.ID
	switcher     *providerSwitcher
	voice        *voiceConfigurator
	currentModel string
}

func buildCommandSuite(cfg config.Config, paths config.PathSet, sessionID core.ID, session *chatSession, switcher *providerSwitcher, voice *voiceConfigurator) *commandSuite {
	return &commandSuite{
		cfg: cfg, paths: paths, sessionID: sessionID, session: session, switcher: switcher, voice: voice,
		currentModel: cfg.Provider.Profiles[cfg.Provider.Active].Model,
	}
}

func (s *commandSuite) selectSessionModel(model string) {
	s.session.conversation.SelectModel(model)
	s.mu.Lock()
	s.currentModel = model
	s.mu.Unlock()
}

func (s *commandSuite) switchProvider(profile string) (terminal.ProviderSwitch, error) {
	result, err := s.switcher.SwitchProvider(profile)
	if err == nil && !result.IsKeyNeeded {
		s.mu.Lock()
		s.currentModel = result.Model
		s.cfg.Provider.Active = profile
		s.mu.Unlock()
	}
	return result, err
}

func (s *commandSuite) switchProviderWithKey(profile, key string) (terminal.ProviderSwitch, error) {
	result, err := s.switcher.SwitchProviderWithKey(profile, key)
	if err == nil {
		s.mu.Lock()
		s.currentModel = result.Model
		s.cfg.Provider.Active = profile
		s.mu.Unlock()
	}
	return result, err
}

func (s *commandSuite) buildInspectionActions() terminal.InspectionActions {
	return terminal.InspectionActions{
		ReadStatus: s.readStatus, ReadPermissions: s.readPermissions,
		ReadInstructions: s.readInstructions, ReadContext: s.readContext,
		RunDoctor: s.runDoctor, ReadSession: s.readSession,
		RevokePermission: s.revokePermission,
	}
}

func (s *commandSuite) buildMemoryActions() terminal.MemoryActions {
	editor, ok := s.session.memoryStore.(core.MemoryEditor)
	if !ok {
		return terminal.MemoryActions{}
	}
	return terminal.MemoryActions{
		Read: func(ctx context.Context) (terminal.MemoryDocument, error) {
			doc, err := editor.ReadMemoryDocument(ctx)
			return buildTerminalMemoryDocument(doc), err
		},
		Change: func(ctx context.Context, change terminal.MemoryChange) (terminal.MemoryDocument, error) {
			kind := map[terminal.MemoryChangeKind]core.MemoryChangeKind{
				terminal.MemoryAppend: core.MemoryAppendTrusted, terminal.MemoryReplace: core.MemoryReplaceTrusted,
				terminal.MemoryClear: core.MemoryClearTrusted,
			}[change.Kind]
			doc, err := editor.ChangeMemory(ctx, core.MemoryChange{Kind: kind, Content: change.Content, ExpectedRevision: change.ExpectedRevision})
			return buildTerminalMemoryDocument(doc), err
		},
	}
}

func buildTerminalMemoryDocument(doc core.MemoryDocument) terminal.MemoryDocument {
	return terminal.MemoryDocument{
		Trusted: doc.Trusted, Untrusted: doc.Untrusted, Revision: doc.Revision,
		Bytes: doc.Bytes, Lines: doc.Lines, MaxBytes: doc.MaxBytes, MaxLines: doc.MaxLines,
	}
}

func (s *commandSuite) buildSettingsActions() terminal.SettingsActions {
	return terminal.SettingsActions{Load: s.loadSettings, Apply: s.applySetting}
}

func (s *commandSuite) loadSettings(context.Context) (terminal.SettingsSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildSettingsSnapshot(), nil
}

func (s *commandSuite) buildSettingsSnapshot() terminal.SettingsSnapshot {
	active := s.switcher.ReportActiveProvider()
	profile := s.cfg.Provider.Profiles[active]
	voice := s.voice.reportConfiguration()
	modelScope := "saved; validates endpoint; resets metadata"
	if s.currentModel != profile.Model {
		modelScope = "this session; edit validates and resets metadata"
	}
	return terminal.SettingsSnapshot{StartDictation: voice.StartDictation, Rows: []terminal.SettingRow{
		{Key: "theme", Label: "Theme", Value: s.cfg.UI.Theme, Scope: "saved", Kind: terminal.SettingChoice, Choices: []string{"auto", "dark", "light", "high_contrast", "mono"}},
		{Key: "reduced_motion", Label: "Reduced motion", Value: strconv.FormatBool(s.cfg.UI.ReducedMotion), Scope: "saved", Kind: terminal.SettingToggle},
		{Key: "voice_input", Label: "Voice input", Value: reportOnOff(voice.IsInputEnabled), Scope: "saved", Kind: terminal.SettingToggle},
		{Key: "voice_output", Label: "Voice output", Value: reportOnOff(voice.IsOutputEnabled), Scope: "saved", Kind: terminal.SettingToggle},
		{Key: "voice_key", Label: "Push-to-talk key", Value: voice.PushToTalkKey, Scope: "saved", Kind: terminal.SettingText},
		{Key: "max_turn_cost", Label: "Turn cost limit", Value: strconv.FormatFloat(s.cfg.Brain.MaxTurnCostUSD, 'f', -1, 64), Scope: "saved", Kind: terminal.SettingText},
		{Key: "max_daily_cost", Label: "Daily cost limit", Value: strconv.FormatFloat(s.cfg.Brain.MaxDailyCostUSD, 'f', -1, 64), Scope: "saved", Kind: terminal.SettingText},
		{Key: "retention", Label: "Session retention", Value: s.cfg.Session.Retention, Scope: "saved; next sweep", Kind: terminal.SettingText},
		{Key: "provider", Label: "Provider", Value: active, Scope: "saved", Kind: terminal.SettingChoice, Choices: s.switcher.ListProviders()},
		{Key: "model", Label: "Model", Value: s.currentModel, Scope: modelScope, Kind: terminal.SettingText},
	}}
}

func reportOnOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func (s *commandSuite) applySetting(ctx context.Context, change terminal.SettingChange) (terminal.SettingsSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch change.Key {
	case "theme":
		next := s.cfg.UI
		next.Theme = change.Value
		if err := config.SaveUI(s.paths, next); err != nil {
			return terminal.SettingsSnapshot{}, err
		}
		s.cfg.UI = next
	case "reduced_motion":
		value, err := strconv.ParseBool(change.Value)
		if err != nil {
			return terminal.SettingsSnapshot{}, fmt.Errorf("reduced motion must be true or false: %w", core.ErrInvalid)
		}
		next := s.cfg.UI
		next.ReducedMotion = value
		if err := config.SaveUI(s.paths, next); err != nil {
			return terminal.SettingsSnapshot{}, err
		}
		s.cfg.UI = next
	case "voice_input", "voice_output":
		value := change.Value == "true" || change.Value == "on"
		voiceChange := terminal.VoiceChange{}
		if change.Key == "voice_input" {
			voiceChange.InputEnabled = &value
		} else {
			voiceChange.OutputEnabled = &value
		}
		if _, err := s.voice.Configure(voiceChange); err != nil {
			return terminal.SettingsSnapshot{}, err
		}
	case "voice_key":
		value := strings.TrimSpace(change.Value)
		if _, err := s.voice.Configure(terminal.VoiceChange{PushToTalkKey: &value}); err != nil {
			return terminal.SettingsSnapshot{}, err
		}
	case "max_turn_cost", "max_daily_cost":
		value, err := strconv.ParseFloat(strings.TrimSpace(change.Value), 64)
		if err != nil || value < 0 {
			return terminal.SettingsSnapshot{}, fmt.Errorf("cost limit must be a non-negative number: %w", core.ErrInvalid)
		}
		turn, daily := s.cfg.Brain.MaxTurnCostUSD, s.cfg.Brain.MaxDailyCostUSD
		if change.Key == "max_turn_cost" {
			turn = value
		} else {
			daily = value
		}
		if err := s.session.conversation.SetBudgetLimits(turn, daily); err != nil {
			return terminal.SettingsSnapshot{}, fmt.Errorf("wait for the current turn to finish: %w", err)
		}
		if err := config.SaveCostBudgets(s.paths, turn, daily); err != nil {
			_ = s.session.conversation.SetBudgetLimits(s.cfg.Brain.MaxTurnCostUSD, s.cfg.Brain.MaxDailyCostUSD)
			return terminal.SettingsSnapshot{}, err
		}
		s.cfg.Brain.MaxTurnCostUSD, s.cfg.Brain.MaxDailyCostUSD = turn, daily
	case "retention":
		if err := config.SaveSessionRetention(s.paths, strings.TrimSpace(change.Value)); err != nil {
			return terminal.SettingsSnapshot{}, err
		}
		s.cfg.Session.Retention = strings.TrimSpace(change.Value)
	case "provider":
		result, err := s.switcher.SwitchProvider(change.Value)
		if err != nil {
			return terminal.SettingsSnapshot{}, err
		}
		if result.IsKeyNeeded {
			return terminal.SettingsSnapshot{}, fmt.Errorf("provider %q needs a key; use /provider: %w", change.Value, core.ErrInvalid)
		}
		s.cfg.Provider.Active = change.Value
		s.currentModel = result.Model
	case "model":
		model := strings.TrimSpace(change.Value)
		active := s.switcher.ReportActiveProvider()
		if model == "" {
			return terminal.SettingsSnapshot{}, fmt.Errorf("model is required: %w", core.ErrInvalid)
		}
		models, err := s.switcher.ListModels(ctx)
		if err != nil {
			return terminal.SettingsSnapshot{}, fmt.Errorf("list models before saving: %w", err)
		}
		if !slices.Contains(models, model) {
			return terminal.SettingsSnapshot{}, fmt.Errorf("model %q is not offered by the active provider: %w", model, core.ErrInvalid)
		}
		previous := s.cfg.Provider.Profiles[active]
		if err := s.switcher.ChangeModel(model, loop.Pricing{}, 0); err != nil {
			return terminal.SettingsSnapshot{}, err
		}
		savedProfile := previous
		savedProfile.Model, savedProfile.ContextLength, savedProfile.MaxTokens = model, 0, 0
		savedProfile.InputUSDPerMTok, savedProfile.OutputUSDPerMTok = 0, 0
		if err := config.SaveProviderModel(s.paths, active, savedProfile); err != nil {
			_ = s.switcher.ChangeModel(previous.Model, loop.Pricing{
				InputUSDPerMTok: previous.InputUSDPerMTok, OutputUSDPerMTok: previous.OutputUSDPerMTok,
			}, previous.ContextLength)
			return terminal.SettingsSnapshot{}, err
		}
		s.cfg.Provider.Profiles[active] = savedProfile
		s.currentModel = model
	default:
		return terminal.SettingsSnapshot{}, fmt.Errorf("unknown setting %q: %w", change.Key, core.ErrInvalid)
	}
	return s.buildSettingsSnapshot(), nil
}

func (s *commandSuite) readStatus(ctx context.Context) (terminal.InspectionReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	active := s.switcher.ReportActiveProvider()
	snapshot := s.session.conversation.ReportContextSnapshot()
	mcpStatuses := s.session.mcpManager.ReportServerStatuses()
	mcpRows := []terminal.ReportRow{{Label: "Tool servers", Value: "None configured", Status: terminal.ReportUnavailable}}
	if len(mcpStatuses) != 0 {
		mcpRows = make([]terminal.ReportRow, 0, len(mcpStatuses))
		for _, status := range mcpStatuses {
			mcpRows = append(mcpRows, terminal.ReportRow{
				Label: status.Name, Value: describeServiceState(string(status.State)), Detail: status.Reason,
				Status: reportServiceStatus(string(status.State)),
			})
		}
	}
	voice := s.voice.reportConfiguration()
	sandboxState := "unavailable"
	if tier, err := sandbox.ReportAvailability(); err == nil {
		sandboxState = string(tier)
	}
	spentToday := "unknown"
	if spent, err := s.session.spendLedger.ReportSpentToday(ctx); err == nil {
		spentToday = fmt.Sprintf("$%.4f", spent)
	}
	return terminal.InspectionReport{
		Summary: "Current Travis configuration",
		Sections: []terminal.ReportSection{
			{Title: "Model", Rows: []terminal.ReportRow{
				{Label: "Provider", Value: active, Status: terminal.ReportCurrent},
				{Label: "Model", Value: s.currentModel},
			}},
			{Title: "Usage and limits", Rows: []terminal.ReportRow{
				{Label: "Context used", Value: fmt.Sprintf("%d estimated tokens", snapshot.EstimatedRequestTokens)},
				{Label: "Context limit", Value: reportOptionalInt(snapshot.ContextLength)},
				{Label: "Spent today", Value: spentToday},
				{Label: "Turn cost limit", Value: reportMoneyLimit(s.cfg.Brain.MaxTurnCostUSD)},
				{Label: "Daily cost limit", Value: reportMoneyLimit(s.cfg.Brain.MaxDailyCostUSD)},
			}},
			{Title: "Safety", Rows: []terminal.ReportRow{
				{Label: "Security profile", Value: s.cfg.Security.Profile},
				{Label: "Sandbox", Value: sandboxState, Detail: reportSandboxRequirement(s.cfg.Security.RequireSandbox)},
			}},
			{Title: "Interface", Rows: []terminal.ReportRow{
				{Label: "Theme", Value: s.cfg.UI.Theme},
				{Label: "Reduced motion", Value: reportEnabledDisabled(s.cfg.UI.ReducedMotion)},
				{Label: "Voice input", Value: reportEnabledDisabled(voice.IsInputEnabled)},
				{Label: "Voice output", Value: reportEnabledDisabled(voice.IsOutputEnabled)},
				{Label: "Push-to-talk key", Value: voice.PushToTalkKey},
			}},
			{Title: "Tool servers", Rows: mcpRows},
		},
		Details: []terminal.ReportRow{{Label: "Session ID", Value: string(s.sessionID), Kind: terminal.ReportValueIdentifier}},
	}, nil
}

func reportOptionalInt(value int) string {
	if value <= 0 {
		return "Not reported"
	}
	return fmt.Sprintf("%d tokens", value)
}

func reportMoneyLimit(value float64) string {
	if value <= 0 {
		return "No limit"
	}
	return fmt.Sprintf("$%.2f", value)
}

func reportSandboxRequirement(isRequired bool) string {
	if isRequired {
		return "Required for tool servers."
	}
	return "Optional for tool servers."
}

func reportEnabledDisabled(isEnabled bool) string {
	if isEnabled {
		return "Enabled"
	}
	return "Disabled"
}

func describeServiceState(state string) string {
	switch strings.ToLower(state) {
	case "running", "ready", "connected":
		return "Ready"
	case "starting":
		return "Starting"
	case "stopped":
		return "Stopped"
	case "failed", "failed to start", "unavailable":
		return "Unavailable"
	default:
		return state
	}
}

func reportServiceStatus(state string) terminal.ReportStatus {
	switch strings.ToLower(state) {
	case "running", "ready", "connected":
		return terminal.ReportSuccess
	case "starting":
		return terminal.ReportLoading
	case "failed", "failed to start", "unavailable":
		return terminal.ReportError
	default:
		return terminal.ReportNeutral
	}
}

func describeInstructionKind(kind string) string {
	switch kind {
	case "agents":
		return "Project instructions"
	case "skill-index":
		return "Skill instructions"
	default:
		return "Additional instructions"
	}
}

func describeInstructionStatus(status string) string {
	if status == "active" {
		return "Active"
	}
	return status
}

func reportInstructionStatus(status string) terminal.ReportStatus {
	if status == "active" {
		return terminal.ReportSuccess
	}
	return terminal.ReportNeutral
}

func describeTrustTier(tier string) string {
	switch tier {
	case "workspace":
		return "Trusted project file"
	case "user":
		return "Trusted user file"
	default:
		return tier
	}
}

func (s *commandSuite) readPermissions(ctx context.Context) (terminal.InspectionReport, error) {
	reporter, ok := s.session.memoryStore.(interface {
		ReportSecuritySnapshot(context.Context) (broker.SecuritySnapshot, error)
	})
	if !ok {
		return terminal.InspectionReport{}, fmt.Errorf("broker security inspection is unavailable: %w", core.ErrUnsupported)
	}
	snapshot, err := reporter.ReportSecuritySnapshot(ctx)
	if err != nil {
		return terminal.InspectionReport{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	accessRows := []terminal.ReportRow{{Label: "Security profile", Value: s.cfg.Security.Profile, Status: terminal.ReportCurrent}}
	for _, grant := range snapshot.Grants {
		accessRows = append(accessRows, terminal.ReportRow{
			Label: "Allowed", Value: string(grant.Kind), Detail: grant.Scope, Status: terminal.ReportSuccess,
		})
	}
	for _, deny := range snapshot.Denies {
		accessRows = append(accessRows, terminal.ReportRow{
			Label: "Blocked", Value: string(deny.Kind), Detail: deny.Scope, Status: terminal.ReportWarning,
		})
	}
	ruleRows := make([]terminal.ReportRow, 0, len(snapshot.Rules))
	if len(snapshot.Rules) == 0 {
		ruleRows = append(ruleRows, terminal.ReportRow{
			Label: "Remembered permissions", Value: "None", Status: terminal.ReportUnavailable,
		})
	}
	for _, summary := range snapshot.Rules {
		ruleRows = append(ruleRows, terminal.ReportRow{
			Label: "Allowed " + string(summary.Rule.Kind), Value: summary.Rule.Scope,
			Detail: "Rule ID: " + summary.ID, Status: terminal.ReportSuccess,
		})
	}
	denialRows := make([]terminal.ReportRow, 0, len(snapshot.RecentDenials))
	for _, denial := range snapshot.RecentDenials {
		denialRows = append(denialRows, terminal.ReportRow{
			Label: "Blocked recently", Value: denial, Status: terminal.ReportWarning,
		})
	}
	if len(denialRows) == 0 {
		denialRows = append(denialRows, terminal.ReportRow{Label: "Recent blocks", Value: "None"})
	}
	return terminal.InspectionReport{
		Summary: "What Travis can access in this session",
		Sections: []terminal.ReportSection{
			{Title: "Effective access", Rows: accessRows},
			{Title: "Remembered permissions", Rows: ruleRows},
			{Title: "Recent blocks", Rows: denialRows},
		},
		Details: []terminal.ReportRow{
			{Label: "Managed memory", Value: "The security broker controls reads and writes."},
			{Label: "Commands", Value: "Always ask before execution."},
			{Label: "Untrusted content", Value: "Cannot reuse remembered permissions."},
			{Label: "Voice capture", Value: "Controlled by the terminal, outside the security broker."},
		},
	}, nil
}

func (s *commandSuite) revokePermission(ctx context.Context, id string) error {
	revoker, ok := s.session.memoryStore.(interface {
		RevokeDecisionRule(context.Context, string) error
	})
	if !ok {
		return fmt.Errorf("broker permission revocation is unavailable: %w", core.ErrUnsupported)
	}
	return revoker.RevokeDecisionRule(ctx, id)
}

func (s *commandSuite) readInstructions(context.Context) (terminal.InspectionReport, error) {
	activeRows := []terminal.ReportRow{{
		Label: "Travis built-in rules", Value: "Always active", Status: terminal.ReportSuccess,
	}}
	var details []terminal.ReportRow
	for _, source := range s.session.skillsCatalog.ReportInstructionSources() {
		label := source.Name
		if label == "" {
			label = describeInstructionKind(source.Kind)
		}
		activeRows = append(activeRows, terminal.ReportRow{
			Label: label, Value: describeInstructionStatus(source.Status),
			Detail: describeInstructionKind(source.Kind), Status: reportInstructionStatus(source.Status),
		})
		if source.Path != "" {
			details = append(details, terminal.ReportRow{
				Label: label + " source", Value: source.Path, Kind: terminal.ReportValuePath,
			})
		}
		details = append(details, terminal.ReportRow{
			Label: label + " trust", Value: describeTrustTier(source.Tier),
		})
		if source.Bytes > 0 {
			details = append(details, terminal.ReportRow{
				Label: label + " size", Value: fmt.Sprintf("%d bytes", source.Bytes),
			})
		}
		if source.Hash != "" {
			details = append(details, terminal.ReportRow{
				Label: label + " fingerprint", Value: abbreviateIdentifier(source.Hash), Kind: terminal.ReportValueIdentifier,
			})
		}
	}
	memoryPath := filepath.Join(s.paths.StateDir, "memory.md")
	return terminal.InspectionReport{
		Summary: "Instructions Travis can use",
		Sections: []terminal.ReportSection{
			{Title: "Active now", Rows: activeRows},
			{Title: "Available when needed", Rows: []terminal.ReportRow{{
				Label: "Saved memory", Value: "Opened only when a task needs it",
				Detail: "It is not added to every prompt.", Status: terminal.ReportNeutral,
			}}},
		},
		Details: append(details,
			terminal.ReportRow{Label: "Memory source", Value: memoryPath, Kind: terminal.ReportValuePath},
			terminal.ReportRow{Label: "Memory trust", Value: "May contain both user-written and generated notes"},
		),
	}, nil
}

func abbreviateIdentifier(value string) string {
	const visibleEnd = 8
	if len(value) <= visibleEnd*2+1 {
		return value
	}
	return value[:visibleEnd] + "…" + value[len(value)-visibleEnd:]
}

func (s *commandSuite) readContext(context.Context) (terminal.InspectionReport, error) {
	snapshot := s.session.conversation.ReportContextSnapshot()
	if snapshot.Model == "" {
		return terminal.InspectionReport{
			Summary: "No model request has been sent in this session yet.",
		}, nil
	}
	usage := "Not reported by the provider"
	if snapshot.HasProviderInputTokens {
		usage = fmt.Sprintf("%d tokens", snapshot.ProviderInputTokens)
	}
	summaryUsage := "None reported"
	if snapshot.HasSummarizerUsage {
		summaryUsage = fmt.Sprintf("%d input + %d output tokens", snapshot.SummarizerInputTokens, snapshot.SummarizerOutputTokens)
	}
	return terminal.InspectionReport{
		Summary: "What the latest model request contained",
		Sections: []terminal.ReportSection{
			{Title: "Usage", Rows: []terminal.ReportRow{
				{Label: "Model", Value: snapshot.Model, Status: terminal.ReportCurrent},
				{Label: "Estimated request size", Value: fmt.Sprintf("%d tokens", snapshot.EstimatedRequestTokens)},
				{Label: "Context limit", Value: reportOptionalInt(snapshot.ContextLength)},
				{Label: "Provider-reported input", Value: usage},
			}},
			{Title: "What used the context", Rows: []terminal.ReportRow{
				{Label: "Instructions", Value: fmt.Sprintf("about %d tokens", snapshot.SystemBytes/4)},
				{Label: "Conversation", Value: fmt.Sprintf("about %d tokens across %d messages", snapshot.MessageBytes/4, snapshot.MessageCount)},
				{Label: "Tool descriptions", Value: fmt.Sprintf("about %d tokens across %d tools", snapshot.ToolSchemaBytes/4, snapshot.ToolCount)},
			}},
			{Title: "Automatic cleanup", Rows: []terminal.ReportRow{
				{Label: "Compaction starts at", Value: fmt.Sprintf("%.0f%% of the context limit", snapshot.CompactionThreshold*100)},
				{Label: "Earlier content summarized", Value: fmt.Sprintf("%d blocks and %d entries", snapshot.FoldedBlockCount, snapshot.FoldedEntryCount)},
				{Label: "Summarizer usage", Value: summaryUsage},
			}},
		},
		Details: []terminal.ReportRow{
			{Label: "Exact request size", Value: fmt.Sprintf("%d JSON bytes", snapshot.RequestBytes)},
			{Label: "Items always preserved", Value: strconv.Itoa(snapshot.PreservedItemCount)},
			{Label: "Untrusted tool replies", Value: strconv.Itoa(snapshot.UntrustedReplyCount)},
			{Label: "Untrusted replies removed", Value: strconv.Itoa(snapshot.DroppedUntrustedReplies)},
		},
	}, nil
}

func (s *commandSuite) runDoctor(ctx context.Context, mode terminal.DoctorMode) (terminal.InspectionReport, error) {
	var checks, details []terminal.ReportRow
	addCheck := func(label, value string, status terminal.ReportStatus, detail string) {
		checks = append(checks, terminal.ReportRow{Label: label, Value: value, Status: status})
		if detail != "" {
			details = append(details, terminal.ReportRow{Label: label + " details", Value: detail})
		}
	}
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	if diagnostics := cfg.Validate(); diagnostics.HasErrors() {
		for _, diagnostic := range diagnostics {
			addCheck("Configuration", "Needs attention", terminal.ReportError, diagnostic.Message)
		}
	} else {
		addCheck("Configuration", "Valid", terminal.ReportSuccess, "")
	}
	if _, err := s.readPermissions(ctx); err != nil {
		addCheck("Security broker", "Not responding", terminal.ReportError, err.Error())
	} else {
		addCheck("Security broker", "Responding", terminal.ReportSuccess, "")
		details = append(details, terminal.ReportRow{Label: "Broker protocol", Value: fmt.Sprint(broker.ProtocolVersion)})
	}
	if _, err := broker.ResolveVerifiedBrokerExecutable(brokerSHA256); err != nil {
		addCheck("Broker executable", "Could not verify", terminal.ReportError, err.Error())
	} else {
		addCheck("Broker executable", "Verified", terminal.ReportSuccess, "")
	}
	if _, err := s.readSession(ctx); err != nil {
		addCheck("Session history", "Not readable", terminal.ReportError, err.Error())
	} else {
		addCheck("Session history", "Readable", terminal.ReportSuccess, "")
	}
	if tier, err := sandbox.ReportAvailability(); err != nil {
		addCheck("Sandbox", "Unavailable", terminal.ReportWarning, err.Error())
	} else {
		addCheck("Sandbox", "Available ("+string(tier)+")", terminal.ReportSuccess, "")
	}
	for _, status := range s.session.mcpManager.ReportServerStatuses() {
		addCheck("Tool server "+status.Name, describeServiceState(string(status.State)), reportServiceStatus(string(status.State)), status.Reason)
	}
	if _, err := voicein.ResolveModuleFiles(); err != nil {
		addCheck("Voice input", "Unavailable", terminal.ReportWarning, err.Error())
	} else {
		addCheck("Voice input", "Ready", terminal.ReportSuccess, "")
	}
	if _, err := voiceout.BuildSystemSpeaker(); err != nil {
		addCheck("Voice output", "Unavailable", terminal.ReportWarning, err.Error())
	} else {
		addCheck("Voice output", "Ready", terminal.ReportSuccess, "")
	}
	if mode == terminal.DoctorLive {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		type localProbeResult struct {
			name       string
			isDetected bool
			models     int
		}
		localResults := make(chan localProbeResult, len(localServerProbes))
		for _, probe := range localServerProbes {
			go func(probe localServerProbe) {
				server := probe.detect(probeCtx, probe.origin)
				localResults <- localProbeResult{name: probe.profile, isDetected: server.IsDetected, models: len(server.Models)}
			}(probe)
		}
		for range localServerProbes {
			result := <-localResults
			if result.isDetected {
				addCheck("Local "+result.name, fmt.Sprintf("Detected with %d models", result.models), terminal.ReportSuccess, "")
			} else {
				addCheck("Local "+result.name, "Not detected", terminal.ReportUnavailable, "")
			}
		}
		models, err := s.switcher.ListModels(probeCtx)
		if err != nil {
			addCheck("Active provider", "Connection failed", terminal.ReportError, err.Error())
		} else {
			addCheck("Active provider", fmt.Sprintf("Connected · %d models available", len(models)), terminal.ReportSuccess, "")
		}
	} else {
		checks = append(checks, terminal.ReportRow{
			Label: "Provider connection", Value: "Not checked",
			Detail: "Run /doctor live to test network connections.", Status: terminal.ReportNeutral,
		})
	}
	return terminal.InspectionReport{
		Summary:  "Travis health check",
		Sections: []terminal.ReportSection{{Title: "Checks", Rows: checks}},
		Details:  details,
	}, nil
}

func (s *commandSuite) readSession(context.Context) (terminal.InspectionReport, error) {
	rows, err := buildSessionRows(buildSessionsDir(s.paths))
	if err != nil {
		return terminal.InspectionReport{}, err
	}
	recent := make([]terminal.ReportRow, 0, min(len(rows), 5))
	details := []terminal.ReportRow{{
		Label: "Current session ID", Value: string(s.sessionID), Kind: terminal.ReportValueIdentifier,
	}}
	first := max(len(rows)-5, 0)
	for i := len(rows) - 1; i >= first; i-- {
		row := rows[i]
		if row.Err != nil {
			recent = append(recent, terminal.ReportRow{
				Label: "Unreadable session", Value: string(row.ID), Status: terminal.ReportError,
			})
			details = append(details, terminal.ReportRow{Label: string(row.ID) + " error", Value: row.Err.Error()})
			continue
		}
		label := row.Started
		if label == "-" {
			label = "Empty session"
		}
		value := fmt.Sprintf("%d turns", row.Turns)
		if row.Prompt != "" {
			value += " · " + row.Prompt
		}
		status := terminal.ReportNeutral
		if row.ID == s.sessionID {
			status = terminal.ReportCurrent
		}
		recent = append(recent, terminal.ReportRow{Label: label, Value: value, Status: status})
		details = append(details, terminal.ReportRow{
			Label: label + " ID", Value: string(row.ID), Kind: terminal.ReportValueIdentifier,
		})
	}
	if len(recent) == 0 {
		recent = append(recent, terminal.ReportRow{Label: "Recent sessions", Value: "None yet"})
	}
	return terminal.InspectionReport{
		Summary: "Current and recent conversations",
		Sections: []terminal.ReportSection{
			{Title: "Current session", Rows: []terminal.ReportRow{{
				Label: "Status", Value: "Active", Status: terminal.ReportCurrent,
			}}},
			{Title: "Recent sessions", Rows: recent},
		},
		Details: details,
	}, nil
}
