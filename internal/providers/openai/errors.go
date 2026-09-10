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
	kind := core.ErrTransient
	switch {
	case status == http.StatusTooManyRequests:
		kind = core.ErrExhausted
	case status >= 400 && status < 500:
		kind = core.ErrInvalid
	}
	return &core.ProviderError{Provider: providerName, Kind: kind, Cause: describeStatusFailure(status, body)}
}

// translateMalformedChunk reports a frame the endpoint sent that is not valid JSON.
// core has no dedicated malformed-response kind, so this lands on ErrInvalid.
func translateMalformedChunk(cause error) error {
	return &core.ProviderError{Provider: providerName, Kind: core.ErrInvalid, Cause: cause}
}

// translateTransportError reports a connection that failed partway through a stream.
func translateTransportError(cause error) error {
	return &core.ProviderError{Provider: providerName, Kind: core.ErrTransient, Cause: cause}
}

func describeStatusFailure(status int, body io.Reader) error {
	snippet, _ := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	detail := strings.TrimSpace(string(snippet))
	if detail == "" {
		return errors.New(describeStatus(status))
	}
	return fmt.Errorf("%s: %s", describeStatus(status), detail)
}
