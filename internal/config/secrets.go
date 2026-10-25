package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/zalando/go-keyring"

	"github.com/Kzeezee/travis/internal/core"
)

// providerAuthEnvVars are the only variables a profile's `auth.ref = "env://NAME"` may name.
// A closed list keeps BuildEnvironmentWithoutSecrets able to strip every credential.
// The first is the fallback, matching DESIGN.md 5.2's example `ref = "env://OPENAI_API_KEY"`.
var providerAuthEnvVars = []string{"OPENAI_API_KEY", "DEEPSEEK_API_KEY", "GEMINI_API_KEY"}

// keyringService namespaces travis's entries in the OS credential store; `keyring://NAME` is account NAME under it.
const keyringService = "travis"

const keyringRefPrefix = "keyring://"

// keyringReader reads one entry from the OS credential store, returning keyring.ErrNotFound when it is absent.
type keyringReader func(service, account string) (string, error)

// ResolveProviderAPIKey reports the credential for the named provider profile, or an empty
// string when none is set. An empty result is normal: local endpoints need no credential.
// The ref's own source wins; a profile whose ref yields nothing falls back to the keyring
// entry named after the profile, which is where SaveProviderAPIKey stores a typed-in key.
// The error is only for a `keyring://` ref the OS credential store could not answer.
//
// Known deviation, accepted for Phase 1. DESIGN.md 5.2 has config carry a secret
// *reference* (`env://`, `keyring://`, `cmd://`) and 5.12.3 resolves it inside the broker,
// so the value never enters the process that runs model output. Resolution still
// happens in this process; moving it into the broker is separate work.
func ResolveProviderAPIKey(profileName string, auth AuthCfg) (string, error) {
	return resolveProviderAPIKey(os.LookupEnv, keyring.Get, profileName, auth)
}

func resolveProviderAPIKey(lookup envLookup, readKeyring keyringReader, profileName string, auth AuthCfg) (string, error) {
	if account, ok := strings.CutPrefix(auth.Ref, keyringRefPrefix); ok {
		return readKeyringCredential(readKeyring, account)
	}
	name := providerAuthEnvVars[0]
	if ref, ok := strings.CutPrefix(auth.Ref, "env://"); ok && slices.Contains(providerAuthEnvVars, ref) {
		name = ref
	}
	if key := readTrimmedEnvValue(lookup, name); key != "" || profileName == "" {
		return key, nil
	}
	// The fallback is best effort: a local profile needs no key, so a store that cannot answer must not stop it.
	key, err := readKeyringCredential(readKeyring, profileName)
	if err != nil {
		return "", nil
	}
	return key, nil
}

// SaveProviderAPIKey stores key in the OS credential store where ResolveProviderAPIKey
// will find it for this profile: the ref's own keyring entry, else one named after the profile.
func SaveProviderAPIKey(profileName string, auth AuthCfg, key string) error {
	account := selectKeyringAccount(profileName, auth)
	if strings.TrimSpace(account) == "" {
		return fmt.Errorf("provider profile has no name to store its key under: %w", core.ErrInvalid)
	}
	if err := keyring.Set(keyringService, account, strings.TrimSpace(key)); err != nil {
		return fmt.Errorf("save %s%s to the OS credential store: %w", keyringRefPrefix, account, err)
	}
	return nil
}

func selectKeyringAccount(profileName string, auth AuthCfg) string {
	if account, ok := strings.CutPrefix(auth.Ref, keyringRefPrefix); ok {
		return account
	}
	return profileName
}

// ReportIsCredentialRequired is false for an endpoint on this machine, which needs no key.
func ReportIsCredentialRequired(profile ProviderProfileCfg) bool {
	endpoint, err := url.Parse(profile.BaseURL)
	if err != nil {
		return true
	}
	host := endpoint.Hostname()
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

// readKeyringCredential treats a missing entry like an unset variable; a store that cannot answer is an error.
func readKeyringCredential(readKeyring keyringReader, account string) (string, error) {
	if strings.TrimSpace(account) == "" {
		return "", fmt.Errorf("auth.ref %q names no keyring entry", keyringRefPrefix+account)
	}
	secret, err := readKeyring(keyringService, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s%s from the OS credential store: %w", keyringRefPrefix, account, err)
	}
	return strings.TrimSpace(secret), nil
}

// BuildEnvironmentWithoutSecrets is this process's environment minus every provider credential,
// for a child process, such as the broker, that has no use for it.
func BuildEnvironmentWithoutSecrets() []string {
	return removeProviderCredential(os.Environ())
}

// removeProviderCredential matches names case-insensitively, as Windows looks them up.
func removeProviderCredential(environ []string) []string {
	return slices.DeleteFunc(environ, func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return slices.ContainsFunc(providerAuthEnvVars, func(secret string) bool { return strings.EqualFold(name, secret) })
	})
}
