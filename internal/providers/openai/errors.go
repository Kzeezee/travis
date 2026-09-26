package openai

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

// maxErrorBodyBytes caps how much of a failure response is quoted back. The body is the
// endpoint's own error text; the request and its Authorization header are never included.
const maxErrorBodyBytes = 2 << 10

// translateStatusError maps a non-200 response onto a Travis error kind so callers decide
// what to do without knowing HTTP.
func translateStatusError(status int, body io.Reader) error {
	detail := readStatusFailureDetail(body)
	kind := selectStatusErrorKind(status, detail)
	return &core.ProviderError{Provider: providerName, Kind: kind, Cause: describeStatusFailure(status, detail)}
}

func selectStatusErrorKind(status int, detail string) error {
	kind := core.ErrTransient
	switch {
	case reportsContextLengthFailureFromStatusOrBody(status, detail):
		kind = core.ErrContextLength
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = core.ErrAuth
	case status == http.StatusTooManyRequests:
		kind = core.ErrRateLimit
	case status >= 400 && status < 500:
		kind = core.ErrInvalid
	}
	return kind
}

// translateMalformedChunk reports a frame the endpoint sent that is not valid JSON.
func translateMalformedChunk(cause error) error {
	return &core.ProviderError{Provider: providerName, Kind: core.ErrProviderMalformed, Cause: cause}
}

// translateTransportError reports a connection that failed partway through a stream.
func translateTransportError(cause error) error {
	return &core.ProviderError{Provider: providerName, Kind: core.ErrTransient, Cause: cause}
}

func readStatusFailureDetail(body io.Reader) string {
	snippet, _ := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	return strings.TrimSpace(string(snippet))
}

func describeStatusFailure(status int, detail string) error {
	if detail == "" {
		return errors.New(describeStatus(status))
	}
	return fmt.Errorf("%s: %s", describeStatus(status), detail)
}

func reportsContextLengthFailureFromStatusOrBody(status int, detail string) bool {
	if status == http.StatusRequestEntityTooLarge {
		return true
	}

	normalizedDetail := strings.ToLower(detail)
	return strings.Contains(normalizedDetail, "context_length_exceeded") ||
		strings.Contains(normalizedDetail, "context length") ||
		strings.Contains(normalizedDetail, "maximum context") ||
		strings.Contains(normalizedDetail, "too many tokens")
}
