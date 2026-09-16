package config

import "os"

// providerAuthEnvVar matches the reference DESIGN.md 5.2 uses in its own example,
// `ref = "env://OPENAI_API_KEY"`.
const providerAuthEnvVar = "OPENAI_API_KEY"

// ResolveProviderAPIKey reports the model-provider credential, or an empty string when
// none is set. An empty result is normal: local endpoints need no credential.
//
// Known deviation, accepted for Phase 1. DESIGN.md 5.2 has config carry a secret
// *reference* (`env://`, `keyring://`, `cmd://`) and 5.12.3 resolves it inside the broker,
// so the value never enters the process that runs model output. The broker arrives in
// Phase 2; until then the credential lives in this process.
func ResolveProviderAPIKey() string {
	return resolveProviderAPIKey(os.LookupEnv)
}

func resolveProviderAPIKey(lookup envLookup) string {
	return readTrimmedEnvValue(lookup, providerAuthEnvVar)
}
