package core

import "errors"

var (
	ErrInvalid     = errors.New("invalid")
	ErrUnsupported = errors.New("unsupported")
	ErrTransient   = errors.New("transient")
	ErrExhausted   = errors.New("exhausted")
)

type ProviderError struct {
	// Provider names the adapter that reported the error.
	Provider string
	// Kind is the stable Travis error category used with errors.Is.
	Kind     error
	// Cause is the original lower-level error exposed through Unwrap.
	Cause    error
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
