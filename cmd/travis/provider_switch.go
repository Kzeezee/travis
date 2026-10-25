package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
	"github.com/Kzeezee/travis/internal/security/redact"
)

// modelListingProvider is a provider that can also name its models, for /model suggestions.
type modelListingProvider interface {
	core.Provider
	ListModels(ctx context.Context) ([]string, error)
}

// providerSwitcher moves a chat onto another provider profile between turns. It owns the whole
// switch so the frontend only sends intent: finding the profile's key, storing one the user
// types, scrubbing that key from everything recorded, and remembering the choice for next time.
type providerSwitcher struct {
	profiles     map[string]config.ProviderProfileCfg
	conversation interface {
		SelectProvider(core.Provider, string) error
	}
	redactor      redact.Redactor
	resolveKey    func(profile string, auth config.AuthCfg) (string, error)
	saveKey       func(profile string, auth config.AuthCfg, key string) error
	recordChoice  func(profile string) error
	buildProvider func(baseURL, key string) modelListingProvider

	mu      sync.Mutex
	current modelListingProvider
	active  string
}

// SwitchProvider switches to profile, or reports that it needs a key and switches nothing.
func (s *providerSwitcher) SwitchProvider(profile string) (terminal.ProviderSwitch, error) {
	settings, err := s.findProfile(profile)
	if err != nil {
		return terminal.ProviderSwitch{}, err
	}
	key, err := s.resolveKey(profile, settings.Auth)
	if err != nil {
		return terminal.ProviderSwitch{}, err
	}
	if key == "" && config.ReportIsCredentialRequired(settings) {
		return terminal.ProviderSwitch{IsKeyNeeded: true}, nil
	}
	return s.activate(profile, settings, key)
}

// SwitchProviderWithKey stores key for profile, then switches; an empty key switches without one.
// A key the store refuses still switches this session, with a warning that it was not saved.
func (s *providerSwitcher) SwitchProviderWithKey(profile, key string) (terminal.ProviderSwitch, error) {
	settings, err := s.findProfile(profile)
	if err != nil {
		return terminal.ProviderSwitch{}, err
	}
	key = strings.TrimSpace(key)
	var saveWarning string
	if key != "" {
		s.redactor.AddKnownValue(key) // before the store's error, or anything else, can echo it
		if err := s.saveKey(profile, settings.Auth, key); err != nil {
			saveWarning = "key not saved, so it lasts this session only: " + err.Error()
		}
	}
	result, err := s.activate(profile, settings, key)
	result.Warning = strings.Join(slices.DeleteFunc([]string{saveWarning, result.Warning}, func(w string) bool { return w == "" }), "; ")
	return result, err
}

func (s *providerSwitcher) activate(profile string, settings config.ProviderProfileCfg, key string) (terminal.ProviderSwitch, error) {
	s.redactor.AddKnownValue(key)
	provider := s.buildProvider(settings.BaseURL, key)
	if err := s.conversation.SelectProvider(provider, settings.Model); err != nil {
		return terminal.ProviderSwitch{}, err
	}
	s.mu.Lock()
	s.current, s.active = provider, profile
	s.mu.Unlock()
	result := terminal.ProviderSwitch{Model: settings.Model}
	if err := s.recordChoice(profile); err != nil {
		result.Warning = "next chat starts on the old provider: " + err.Error()
	}
	return result, nil
}

func (s *providerSwitcher) findProfile(profile string) (config.ProviderProfileCfg, error) {
	settings, ok := s.profiles[profile]
	if !ok {
		return config.ProviderProfileCfg{}, fmt.Errorf("no provider profile %q (have %s): %w", profile, strings.Join(s.ListProviders(), ", "), core.ErrInvalid)
	}
	return settings, nil
}

// ListProviders names every configured provider profile, sorted.
func (s *providerSwitcher) ListProviders() []string {
	return slices.Sorted(maps.Keys(s.profiles))
}

// DescribeProviders reports every profile with whether a key is available, never the key itself.
func (s *providerSwitcher) DescribeProviders() []terminal.ProviderSummary {
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	summaries := make([]terminal.ProviderSummary, 0, len(s.profiles))
	for _, name := range s.ListProviders() {
		settings := s.profiles[name]
		key, err := s.resolveKey(name, settings.Auth)
		summaries = append(summaries, terminal.ProviderSummary{
			Name: name, Model: settings.Model, IsActive: name == active,
			IsKeyRequired: config.ReportIsCredentialRequired(settings), HasKey: err == nil && key != "",
		})
	}
	return summaries
}

// ListModels asks whichever provider the chat is on right now.
func (s *providerSwitcher) ListModels(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	provider := s.current
	s.mu.Unlock()
	return provider.ListModels(ctx)
}
