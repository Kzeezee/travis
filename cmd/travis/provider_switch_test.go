package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/redact"
)

type fakeListingProvider struct{ baseURL, key string }

func (fakeListingProvider) Chat(context.Context, core.ProviderRequest) (core.Stream, error) {
	return nil, errors.New("not used")
}

func (p fakeListingProvider) ListModels(context.Context) ([]string, error) {
	return []string{p.baseURL}, nil
}

type recordingConversation struct {
	provider core.Provider
	model    string
	err      error
}

func (c *recordingConversation) SelectProvider(provider core.Provider, model string) error {
	if c.err != nil {
		return c.err
	}
	c.provider, c.model = provider, model
	return nil
}

// buildTestSwitcher stores keys in memory and remembers choices in a slice, never the OS.
func buildTestSwitcher(keys map[string]string) (*providerSwitcher, *recordingConversation, *[]string) {
	conversation := &recordingConversation{}
	var remembered []string
	return &providerSwitcher{
		profiles: map[string]config.ProviderProfileCfg{
			"cloud": {BaseURL: "https://api.example/v1", Model: "cloud-model"},
			"local": {BaseURL: "http://127.0.0.1:8080/v1", Model: "local-model"},
		},
		conversation: conversation,
		redactor:     redact.New(),
		resolveKey:   func(profile string, _ config.AuthCfg) (string, error) { return keys[profile], nil },
		saveKey: func(profile string, _ config.AuthCfg, key string) error {
			keys[profile] = key
			return nil
		},
		recordChoice: func(profile string) error {
			remembered = append(remembered, profile)
			return nil
		},
		buildProvider: func(baseURL, key string) modelListingProvider {
			return fakeListingProvider{baseURL: baseURL, key: key}
		},
		current: fakeListingProvider{baseURL: "startup"},
	}, conversation, &remembered
}

func TestSwitchProviderSwitchesWhenTheKeyIsAvailable(t *testing.T) {
	switcher, conversation, remembered := buildTestSwitcher(map[string]string{"cloud": "sk-stored"})

	result, err := switcher.SwitchProvider("cloud")

	if err != nil || result.IsKeyNeeded || result.Model != "cloud-model" {
		t.Fatalf("got %+v, %v; want a switch to cloud-model", result, err)
	}
	if conversation.provider.(fakeListingProvider).key != "sk-stored" || conversation.model != "cloud-model" {
		t.Fatalf("conversation got %+v / %q, want the stored key and cloud-model", conversation.provider, conversation.model)
	}
	if models, _ := switcher.ListModels(context.Background()); models[0] != "https://api.example/v1" {
		t.Fatalf("ListModels asked %v, want the new provider", models)
	}
	if len(*remembered) != 1 || (*remembered)[0] != "cloud" {
		t.Fatalf("remembered %v, want cloud", *remembered)
	}
}

func TestSwitchProviderAsksForAMissingKeyAndSwitchesNothing(t *testing.T) {
	switcher, conversation, _ := buildTestSwitcher(map[string]string{})

	result, err := switcher.SwitchProvider("cloud")

	if err != nil || !result.IsKeyNeeded || conversation.provider != nil {
		t.Fatalf("got %+v, %v, conversation on %v; want a key request and no switch", result, err, conversation.provider)
	}
}

func TestSwitchProviderNeedsNoKeyForAnEndpointOnThisMachine(t *testing.T) {
	switcher, conversation, _ := buildTestSwitcher(map[string]string{})

	if result, err := switcher.SwitchProvider("local"); err != nil || result.IsKeyNeeded || conversation.model != "local-model" {
		t.Fatalf("got %+v, %v; want a keyless switch to local-model", result, err)
	}
}

func TestSwitchProviderWithKeySavesScrubsAndSwitches(t *testing.T) {
	keys := map[string]string{}
	switcher, conversation, _ := buildTestSwitcher(keys)

	result, err := switcher.SwitchProviderWithKey("cloud", "  sk-typed-in-key  ")

	if err != nil || result.Model != "cloud-model" || result.Warning != "" {
		t.Fatalf("got %+v, %v; want a clean switch", result, err)
	}
	if keys["cloud"] != "sk-typed-in-key" || conversation.provider.(fakeListingProvider).key != "sk-typed-in-key" {
		t.Fatalf("saved %q and used %v, want the trimmed key in both", keys["cloud"], conversation.provider)
	}
	if got := switcher.redactor.RedactText("echo sk-typed-in-key"); strings.Contains(got, "typed-in") {
		t.Fatalf("the typed-in key is not redacted: %q", got)
	}
}

func TestSwitchProviderWithKeyStillSwitchesWhenTheStoreRefuses(t *testing.T) {
	switcher, conversation, _ := buildTestSwitcher(map[string]string{})
	switcher.saveKey = func(string, config.AuthCfg, string) error { return errors.New("keychain locked") }

	result, err := switcher.SwitchProviderWithKey("cloud", "sk-typed-in-key")

	if err != nil || conversation.model != "cloud-model" || !strings.Contains(result.Warning, "keychain locked") {
		t.Fatalf("got %+v, %v; want a switch with a not-saved warning", result, err)
	}
}

func TestSwitchProviderRejectsAnUnknownProfileOrARunningTurn(t *testing.T) {
	switcher, conversation, remembered := buildTestSwitcher(map[string]string{"cloud": "sk"})

	if _, err := switcher.SwitchProvider("nope"); !errors.Is(err, core.ErrInvalid) || !strings.Contains(err.Error(), "cloud, local") {
		t.Fatalf("got %v, want an invalid-profile error listing the profiles", err)
	}
	conversation.err = core.ErrInvalid
	if _, err := switcher.SwitchProvider("cloud"); err == nil || len(*remembered) != 0 {
		t.Fatalf("got %v and remembered %v, want the refusal and nothing remembered", err, *remembered)
	}
}
