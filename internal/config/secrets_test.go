package config

import (
	"errors"
	"slices"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestResolveProviderAPIKeyReadsTheConfiguredEnvironmentValue(t *testing.T) {
	lookup := func(key string) (string, bool) {
		if key != "OPENAI_API_KEY" {
			t.Fatalf("looked up %q, want %q", key, "OPENAI_API_KEY")
		}
		return "  sk-test  ", true
	}

	if got, _ := resolveProviderAPIKey(lookup, refuseKeyring(t), "", AuthCfg{}); got != "sk-test" {
		t.Fatalf("got %q, want the trimmed credential", got)
	}
}

func TestResolveProviderAPIKeyReadsTheVariableTheRefNames(t *testing.T) {
	lookup := func(key string) (string, bool) { return "value-of-" + key, true }

	if got, _ := resolveProviderAPIKey(lookup, refuseKeyring(t), "", AuthCfg{Ref: "env://DEEPSEEK_API_KEY"}); got != "value-of-DEEPSEEK_API_KEY" {
		t.Fatalf("got %q, want the DeepSeek credential", got)
	}
	// An unlisted variable would escape BuildEnvironmentWithoutSecrets, so it is never read.
	if got, _ := resolveProviderAPIKey(lookup, refuseKeyring(t), "", AuthCfg{Ref: "env://HOME"}); got != "value-of-OPENAI_API_KEY" {
		t.Fatalf("got %q, want the fallback credential", got)
	}
}

func TestResolveProviderAPIKeyIsEmptyWhenUnset(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }

	if got, _ := resolveProviderAPIKey(lookup, refuseKeyring(t), "", AuthCfg{}); got != "" {
		t.Fatalf("got %q, want an empty credential", got)
	}
}

func TestResolveProviderAPIKeyReadsTheKeyringEntryTheRefNames(t *testing.T) {
	readKeyring := func(service, account string) (string, error) {
		if service != "travis" || account != "openai" {
			t.Fatalf("read %q/%q, want travis/openai", service, account)
		}
		return " sk-keyring\n", nil
	}

	got, err := resolveProviderAPIKey(refuseEnv(t), readKeyring, "openai", AuthCfg{Ref: "keyring://openai"})
	if err != nil || got != "sk-keyring" {
		t.Fatalf("got %q, %v; want the trimmed keyring credential", got, err)
	}
}

func TestResolveProviderAPIKeyIsEmptyWhenTheKeyringEntryIsMissing(t *testing.T) {
	readKeyring := func(string, string) (string, error) { return "", keyring.ErrNotFound }

	got, err := resolveProviderAPIKey(refuseEnv(t), readKeyring, "openai", AuthCfg{Ref: "keyring://openai"})
	if err != nil || got != "" {
		t.Fatalf("got %q, %v; want an empty credential and no error", got, err)
	}
}

func TestResolveProviderAPIKeyReportsAKeyringThatCannotAnswer(t *testing.T) {
	locked := errors.New("keychain locked")
	readKeyring := func(string, string) (string, error) { return "", locked }

	if _, err := resolveProviderAPIKey(refuseEnv(t), readKeyring, "openai", AuthCfg{Ref: "keyring://openai"}); !errors.Is(err, locked) {
		t.Fatalf("got %v, want the store's error wrapped", err)
	}
	if _, err := resolveProviderAPIKey(refuseEnv(t), refuseKeyring(t), "openai", AuthCfg{Ref: "keyring://"}); err == nil {
		t.Fatal("got no error for a keyring ref with no entry name")
	}
}

// refuseKeyring fails the test if resolution reaches the OS credential store.
func refuseKeyring(t *testing.T) keyringReader {
	return func(service, account string) (string, error) {
		t.Fatalf("read keyring %q/%q, want no keyring access", service, account)
		return "", nil
	}
}

// refuseEnv fails the test if a keyring ref falls back to the environment.
func refuseEnv(t *testing.T) envLookup {
	return func(key string) (string, bool) {
		t.Fatalf("looked up %q, want no environment access", key)
		return "", false
	}
}

func TestRemoveProviderCredentialKeepsEverythingElse(t *testing.T) {
	environ := []string{"PATH=/bin", "OPENAI_API_KEY=sk-test", "openai_api_key=sk-lower", "DEEPSEEK_API_KEY=sk-ds", "GEMINI_API_KEY=g-key", "TRAVIS_HOME=/t", "=C:=C:\\"}

	got := removeProviderCredential(environ)
	if want := []string{"PATH=/bin", "TRAVIS_HOME=/t", "=C:=C:\\"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveProviderAPIKeyFallsBackToTheProfilesKeyringEntry(t *testing.T) {
	unset := func(string) (string, bool) { return "", false }
	readKeyring := func(service, account string) (string, error) {
		if account != "gemini" {
			t.Fatalf("read account %q, want the profile name", account)
		}
		return "typed-in-key", nil
	}

	got, err := resolveProviderAPIKey(unset, readKeyring, "gemini", AuthCfg{Ref: "env://GEMINI_API_KEY"})
	if err != nil || got != "typed-in-key" {
		t.Fatalf("got %q, %v; want the key saved under the profile", got, err)
	}
	// A store that cannot answer must not stop a local profile that needs no key.
	broken := func(string, string) (string, error) { return "", errors.New("no secret service") }
	if got, err := resolveProviderAPIKey(unset, broken, "local", AuthCfg{}); err != nil || got != "" {
		t.Fatalf("got %q, %v; want no key and no error", got, err)
	}
}

func TestReportIsCredentialRequiredIsFalseOnlyForThisMachine(t *testing.T) {
	for baseURL, want := range map[string]bool{
		"http://127.0.0.1:8080/v1":    false,
		"http://localhost:11434/v1":   false,
		"http://[::1]:8080/v1":        false,
		"https://api.openai.com/v1":   true,
		"http://192.168.1.20:8080/v1": true,
	} {
		if got := ReportIsCredentialRequired(ProviderProfileCfg{BaseURL: baseURL}); got != want {
			t.Errorf("%s: got %v, want %v", baseURL, got, want)
		}
	}
}
