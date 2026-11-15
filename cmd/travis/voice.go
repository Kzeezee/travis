package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/Kzeezee/travis/internal/agent/loop"
	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
)

type configurableVoiceOutput interface {
	SetEnabled(isEnabled bool) error
	ReportIsEnabled() bool
}

type configurableSpeechConversation interface {
	SetSpeechEnabled(isEnabled bool) error
}

// One configurator owns runtime changes and persistence so separate UI callbacks cannot leave them out of sync.
type voiceConfigurator struct {
	mu               sync.Mutex
	cfg              config.VoiceCfg
	output           configurableVoiceOutput
	conversation     configurableSpeechConversation
	startDictation   func(context.Context) (func() (string, error), error)
	resolveDictation func(config.VoiceInputCfg) (func(context.Context) (func() (string, error), error), error)
	saveInput        func(config.VoiceInputCfg) error
	saveOutput       func(bool) error
}

func buildVoiceConfigurator(
	paths config.PathSet,
	cfg config.VoiceCfg,
	output configurableVoiceOutput,
	conversation *loop.AssistantConversation,
	startDictation func(context.Context) (func() (string, error), error),
) *voiceConfigurator {
	return &voiceConfigurator{
		cfg: cfg, output: output, conversation: conversation, startDictation: startDictation,
		resolveDictation: resolveDictation,
		saveInput:        func(input config.VoiceInputCfg) error { return config.SaveVoiceInput(paths, input) },
		saveOutput:       func(isEnabled bool) error { return config.SaveVoiceOutput(paths, isEnabled) },
	}
}

func (v *voiceConfigurator) Configure(change terminal.VoiceChange) (terminal.VoiceConfiguration, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if countVoiceChanges(change) != 1 {
		return v.reportConfiguration(), fmt.Errorf("change exactly one voice setting")
	}
	switch {
	case change.InputEnabled != nil:
		if err := v.configureInput(*change.InputEnabled); err != nil {
			return v.reportConfiguration(), err
		}
	case change.OutputEnabled != nil:
		if err := v.configureOutput(*change.OutputEnabled); err != nil {
			return v.reportConfiguration(), err
		}
	case change.PushToTalkKey != nil:
		input := v.cfg.Input
		input.PushToTalkKey = *change.PushToTalkKey
		if err := v.saveInput(input); err != nil {
			return v.reportConfiguration(), err
		}
		v.cfg.Input = input
	}
	return v.reportConfiguration(), nil
}

func countVoiceChanges(change terminal.VoiceChange) int {
	count := 0
	if change.InputEnabled != nil {
		count++
	}
	if change.OutputEnabled != nil {
		count++
	}
	if change.PushToTalkKey != nil {
		count++
	}
	return count
}

func (v *voiceConfigurator) configureInput(isEnabled bool) error {
	input := v.cfg.Input
	input.Enabled = isEnabled
	var startDictation func(context.Context) (func() (string, error), error)
	if isEnabled {
		var err error
		startDictation, err = v.resolveDictation(input)
		if err != nil {
			return err
		}
	}
	if err := v.saveInput(input); err != nil {
		return err
	}
	v.cfg.Input, v.startDictation = input, startDictation
	return nil
}

func (v *voiceConfigurator) configureOutput(isEnabled bool) error {
	wasEnabled := v.output.ReportIsEnabled()
	if err := v.applyOutput(isEnabled); err != nil {
		return err
	}
	if err := v.saveOutput(isEnabled); err != nil {
		if rollbackErr := v.applyOutput(wasEnabled); rollbackErr != nil {
			return fmt.Errorf("save voice output: %w; restore runtime voice: %w", err, rollbackErr)
		}
		return err
	}
	v.cfg.Output.Enabled = isEnabled
	return nil
}

func (v *voiceConfigurator) applyOutput(isEnabled bool) error {
	if isEnabled {
		if err := v.output.SetEnabled(true); err != nil {
			return err
		}
		if err := v.conversation.SetSpeechEnabled(true); err != nil {
			_ = v.output.SetEnabled(false)
			return err
		}
		return nil
	}
	if err := v.conversation.SetSpeechEnabled(false); err != nil {
		return err
	}
	if err := v.output.SetEnabled(false); err != nil {
		_ = v.conversation.SetSpeechEnabled(true)
		return err
	}
	return nil
}

func (v *voiceConfigurator) reportConfiguration() terminal.VoiceConfiguration {
	return terminal.VoiceConfiguration{
		IsInputEnabled: v.startDictation != nil, IsOutputEnabled: v.output.ReportIsEnabled(),
		PushToTalkKey: v.cfg.Input.PushToTalkKey, StartDictation: v.startDictation,
	}
}
