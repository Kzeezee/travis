package config

import (
	"os"
	"slices"
	"strings"
)

// providerAuthEnvVars are the only variables a profile's `auth.ref = "env://NAME"` may name.
// A closed list keeps BuildEnvironmentWithoutSecrets able to strip every credential.
// The first is the fallback, matching DESIGN.md 5.2's example `ref = "env://OPENAI_API_KEY"`.
var providerAuthEnvVars = []string{"OPENAI_API_KEY", "DEEPSEEK_API_KEY", "GEMINI_API_KEY"}

// ResolveProviderAPIKey reports the model-provider credential the profile's auth names, or
// an empty string when none is set. An empty result is normal: local endpoints need no credential.
//
// Known deviation, accepted for Phase 1. DESIGN.md 5.2 has config carry a secret
// *reference* (`env://`, `keyring://`, `cmd://`) and 5.12.3 resolves it inside the broker,
// so the value never enters the process that runs model output. The broker arrives in
// Phase 2; until then the credential lives in this process.
func ResolveProviderAPIKey(auth AuthCfg) string {
	return resolveProviderAPIKey(os.LookupEnv, auth)
}

func resolveProviderAPIKey(lookup envLookup, auth AuthCfg) string {
	name := providerAuthEnvVars[0]
	if ref, ok := strings.CutPrefix(auth.Ref, "env://"); ok && slices.Contains(providerAuthEnvVars, ref) {
		name = ref
	}
	return readTrimmedEnvValue(lookup, name)
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
