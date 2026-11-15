package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
)

type fakeVoiceOutput struct {
	isEnabled bool
	err       error
}

func (o *fakeVoiceOutput) SetEnabled(isEnabled bool) error {
	if o.err != nil {
		return o.err
	}
	o.isEnabled = isEnabled
	return nil
}

func (o *fakeVoiceOutput) ReportIsEnabled() bool { return o.isEnabled }

type fakeSpeechConversation struct {
	isEnabled bool
	err       error
}

func (c *fakeSpeechConversation) SetSpeechEnabled(isEnabled bool) error {
	if c.err != nil {
		return c.err
	}
	c.isEnabled = isEnabled
	return nil
}

func buildTestVoiceConfigurator() (*voiceConfigurator, *fakeVoiceOutput, *fakeSpeechConversation) {
	output := &fakeVoiceOutput{}
	conversation := &fakeSpeechConversation{}
	configurator := &voiceConfigurator{
		cfg:    config.VoiceCfg{Input: config.VoiceInputCfg{PushToTalkKey: "ctrl+t"}},
		output: output, conversation: conversation,
		resolveDictation: func(config.VoiceInputCfg) (func(context.Context) (func() (string, error), error), error) {
			return func(context.Context) (func() (string, error), error) {
				return func() (string, error) { return "words", nil }, nil
			}, nil
		},
		saveInput:  func(config.VoiceInputCfg) error { return nil },
		saveOutput: func(bool) error { return nil },
	}
	return configurator, output, conversation
}

func TestVoiceConfiguratorAppliesAndPersistsEverySetting(t *testing.T) {
	configurator, output, conversation := buildTestVoiceConfigurator()
	var savedInput config.VoiceInputCfg
	var savedOutput bool
	configurator.saveInput = func(input config.VoiceInputCfg) error { savedInput = input; return nil }
	configurator.saveOutput = func(isEnabled bool) error { savedOutput = isEnabled; return nil }

	on := true
	state, err := configurator.Configure(terminal.VoiceChange{InputEnabled: &on})
	if err != nil || !state.IsInputEnabled || !savedInput.Enabled {
		t.Fatalf("enable input = %+v, %v; saved %+v", state, err, savedInput)
	}
	state, err = configurator.Configure(terminal.VoiceChange{OutputEnabled: &on})
	if err != nil || !state.IsOutputEnabled || !savedOutput || !output.isEnabled || !conversation.isEnabled {
		t.Fatalf("enable output = %+v, %v; output=%v speech=%v saved=%v",
			state, err, output.isEnabled, conversation.isEnabled, savedOutput)
	}
	key := "ctrl+r"
	state, err = configurator.Configure(terminal.VoiceChange{PushToTalkKey: &key})
	if err != nil || state.PushToTalkKey != key || savedInput.PushToTalkKey != key {
		t.Fatalf("change key = %+v, %v; saved %+v", state, err, savedInput)
	}
}

func TestVoiceConfiguratorRollsBackRuntimeOutputWhenSavingFails(t *testing.T) {
	configurator, output, conversation := buildTestVoiceConfigurator()
	configurator.saveOutput = func(bool) error { return errors.New("disk full") }
	on := true

	_, err := configurator.Configure(terminal.VoiceChange{OutputEnabled: &on})

	if err == nil || output.isEnabled || conversation.isEnabled {
		t.Fatalf("Configure() error=%v output=%v speech=%v; want failed and rolled back", err, output.isEnabled, conversation.isEnabled)
	}
}

func TestVoiceConfiguratorRejectsMoreThanOneChange(t *testing.T) {
	configurator, _, _ := buildTestVoiceConfigurator()
	on := true
	key := "ctrl+r"

	if _, err := configurator.Configure(terminal.VoiceChange{InputEnabled: &on, PushToTalkKey: &key}); err == nil {
		t.Fatal("Configure() accepted two settings in one change")
	}
}
