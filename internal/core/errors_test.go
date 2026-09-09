package core

import (
	"errors"
	"testing"
)

func TestProviderErrorMatchesKindAndUnwrapsCause(t *testing.T) {
	cause := errors.New("http 429")
	err := &ProviderError{
		Provider: "openai",
		Kind:     ErrExhausted,
		Cause:    cause,
	}

	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("ProviderError should match %v", ErrExhausted)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("ProviderError should unwrap cause %v", cause)
	}
	if got, want := err.Error(), "openai: exhausted"; got != want {
		t.Fatalf("ProviderError.Error() = %q, want %q", got, want)
	}
}
