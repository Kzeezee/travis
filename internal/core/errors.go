package core

import "errors"

var (
	// Note: each provider returns diff error code for the same error, hence no hardcoding by status code
	// ErrInvalid means Travis sent a request the provider cannot accept.
	ErrInvalid = errors.New("invalid")
	// ErrUnsupported means the provider does not support a requested feature.
	ErrUnsupported = errors.New("unsupported")
	// ErrTransient means the provider or transport failed in a way that may succeed later.
	ErrTransient = errors.New("transient")
	// ErrExhausted means a provider-neutral budget, quota, or limit has been consumed.
	ErrExhausted = errors.New("exhausted")
	// ErrAuth means provider credentials or permissions were rejected.
	ErrAuth = errors.New("auth")
	// ErrRateLimit means the provider is throttling requests.
	ErrRateLimit = errors.New("rate_limit")
	// ErrContextLength means the provider rejected the request because the model context is too large.
	ErrContextLength = errors.New("context_length")
	// ErrProviderMalformed means the provider returned data Travis could not parse or trust.
	ErrProviderMalformed = errors.New("provider_malformed")
)

type ProviderError struct {
	// Provider names the adapter that reported the error.
	Provider string
	// Kind is the stable Travis error category used with errors.Is.
	Kind error
	// Cause is the original lower-level error exposed through Unwrap.
	Cause error
}

func (e *ProviderError) Error() string {
	switch {
	case e == nil:
		return "<nil>"
	case e.Provider != "" && e.Kind != nil:
		return e.Provider + ": " + e.Kind.Error()
	case e.Provider != "":
		return e.Provider
	case e.Kind != nil:
		return e.Kind.Error()
	default:
		return "provider error"
	}
}

func (e *ProviderError) Is(target error) bool {
	return e != nil && errors.Is(e.Kind, target)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
