package config

import (
	"slices"
	"testing"
)

func TestResolveProviderAPIKeyReadsTheConfiguredEnvironmentValue(t *testing.T) {
	lookup := func(key string) (string, bool) {
		if key != "OPENAI_API_KEY" {
			t.Fatalf("looked up %q, want %q", key, "OPENAI_API_KEY")
		}
		return "  sk-test  ", true
	}

	if got := resolveProviderAPIKey(lookup, AuthCfg{}); got != "sk-test" {
		t.Fatalf("got %q, want the trimmed credential", got)
	}
}

func TestResolveProviderAPIKeyReadsTheVariableTheRefNames(t *testing.T) {
	lookup := func(key string) (string, bool) { return "value-of-" + key, true }

	if got := resolveProviderAPIKey(lookup, AuthCfg{Ref: "env://DEEPSEEK_API_KEY"}); got != "value-of-DEEPSEEK_API_KEY" {
		t.Fatalf("got %q, want the DeepSeek credential", got)
	}
	// An unlisted variable would escape BuildEnvironmentWithoutSecrets, so it is never read.
	if got := resolveProviderAPIKey(lookup, AuthCfg{Ref: "env://HOME"}); got != "value-of-OPENAI_API_KEY" {
		t.Fatalf("got %q, want the fallback credential", got)
	}
}

func TestResolveProviderAPIKeyIsEmptyWhenUnset(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }

	if got := resolveProviderAPIKey(lookup, AuthCfg{}); got != "" {
		t.Fatalf("got %q, want an empty credential", got)
	}
}

func TestRemoveProviderCredentialKeepsEverythingElse(t *testing.T) {
	environ := []string{"PATH=/bin", "OPENAI_API_KEY=sk-test", "openai_api_key=sk-lower", "DEEPSEEK_API_KEY=sk-ds", "TRAVIS_HOME=/t", "=C:=C:\\"}

	got := removeProviderCredential(environ)
	if want := []string{"PATH=/bin", "TRAVIS_HOME=/t", "=C:=C:\\"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
