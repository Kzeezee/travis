package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/zalando/go-keyring"
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

// ResolveProviderAPIKey reports the model-provider credential the profile's auth names, or
// an empty string when none is set. An empty result is normal: local endpoints need no credential.
// The error is only for a `keyring://` ref the OS credential store could not answer.
//
// Known deviation, accepted for Phase 1. DESIGN.md 5.2 has config carry a secret
// *reference* (`env://`, `keyring://`, `cmd://`) and 5.12.3 resolves it inside the broker,
// so the value never enters the process that runs model output. Resolution still
// happens in this process; moving it into the broker is separate work.
func ResolveProviderAPIKey(auth AuthCfg) (string, error) {
	return resolveProviderAPIKey(os.LookupEnv, keyring.Get, auth)
}

func resolveProviderAPIKey(lookup envLookup, readKeyring keyringReader, auth AuthCfg) (string, error) {
	if account, ok := strings.CutPrefix(auth.Ref, keyringRefPrefix); ok {
		return readKeyringCredential(readKeyring, account)
	}
	name := providerAuthEnvVars[0]
	if ref, ok := strings.CutPrefix(auth.Ref, "env://"); ok && slices.Contains(providerAuthEnvVars, ref) {
		name = ref
	}
	return readTrimmedEnvValue(lookup, name), nil
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
