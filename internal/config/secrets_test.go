package config

import "testing"

func TestResolveProviderAPIKeyReadsTheConfiguredEnvironmentValue(t *testing.T) {
	lookup := func(key string) (string, bool) {
		if key != providerAuthEnvVar {
			t.Fatalf("looked up %q, want %q", key, providerAuthEnvVar)
		}
		return "  sk-test  ", true
	}

	if got := resolveProviderAPIKey(lookup); got != "sk-test" {
		t.Fatalf("got %q, want the trimmed credential", got)
	}
}

func TestResolveProviderAPIKeyIsEmptyWhenUnset(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }

	if got := resolveProviderAPIKey(lookup); got != "" {
		t.Fatalf("got %q, want an empty credential", got)
	}
}
