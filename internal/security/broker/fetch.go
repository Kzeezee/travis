package broker

import (
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/policy"
)

const (
	// fetchTimeout bounds one page fetch (§9.3 time cap). It stays under the client's exchange
	// timeout, so a slow page is reported as a failed fetch rather than breaking the connection.
	fetchTimeout = 20 * time.Second
	// maxRedirectHops bounds a redirect chain (§9.3). Each hop is re-validated, so this is a
	// bound on effort rather than on trust.
	maxRedirectHops = 5
)

var _ core.PageFetcher = (*PolicyEnforcedBroker)(nil)

// FetchPage reads one page over HTTPS, under §9.3. Four things hold at once: the host must be
// granted, the address it resolves to must be public, every redirect hop is checked again, and
// the body is capped in both size and time.
//
// The page's text is the least trusted content travis handles — it is written by whoever owns
// that server. Nothing here tries to make it safe to obey; it is returned for the tool to wrap
// as untrusted, which is what makes the next effect ask (§5.12.4).
func (b *PolicyEnforcedBroker) FetchPage(ctx context.Context, rawURL string) (core.FetchedPage, error) {
	if err := ctx.Err(); err != nil {
		return core.FetchedPage{}, err
	}
	target, err := validateFetchURL(rawURL)
	if err != nil {
		return core.FetchedPage{}, err
	}
	// Authorized before a single packet moves, on the host as written; the address it resolves
	// to is checked separately, at dial time, because that is the only moment it is knowable.
	if err := b.authorize(ctx, core.CapabilityNetworkHTTP, target.Hostname()); err != nil {
		b.approvals.settleOnce(err)
		return core.FetchedPage{}, err
	}
	page, err := b.fetchAuthorizedPage(ctx, target)
	b.approvals.settleOnce(err)
	return page, err
}

// validateFetchURL is fetch's trust boundary. HTTPS only, since a plain-HTTP page can be
// rewritten in flight by anyone on the path, and credentials in the URL are refused: they would
// be sent to a host the user granted for reading, and would reach the broker's log.
func validateFetchURL(rawURL string) (*url.URL, error) {
	if len(rawURL) > MaxFetchURLBytes {
		return nil, fmt.Errorf("url of %d bytes exceeds %d: %w", len(rawURL), MaxFetchURLBytes, core.ErrInvalid)
	}
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("%q is not a url: %w", rawURL, errorsJoinInvalid(err))
	}
	switch {
	case target.Scheme != "https":
		return nil, fmt.Errorf("%q is not https, and only https may be fetched (§9.3): %w", rawURL, core.ErrDenied)
	case target.Hostname() == "":
		return nil, fmt.Errorf("%q names no host: %w", rawURL, core.ErrInvalid)
	case target.User != nil:
		return nil, fmt.Errorf("a url may not carry credentials: %w", core.ErrDenied)
	}
	return target, nil
}

func errorsJoinInvalid(err error) error {
	return fmt.Errorf("%w: %w", core.ErrInvalid, err)
}

func (b *PolicyEnforcedBroker) fetchAuthorizedPage(ctx context.Context, target *url.URL) (core.FetchedPage, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return core.FetchedPage{}, fmt.Errorf("%q: %w", target.Redacted(), errorsJoinInvalid(err))
	}
	request.Header.Set("Accept", "text/html, text/plain;q=0.9, */*;q=0.1")

	response, err := b.buildFetchClient().Do(request)
	if err != nil {
		return core.FetchedPage{}, translateFetchError(target, err)
	}
	defer response.Body.Close()

	contentType := response.Header.Get("Content-Type")
	if !isReadableContentType(contentType) {
		return core.FetchedPage{}, fmt.Errorf("%q served %q, which is not text travis can read: %w", target.Redacted(), contentType, core.ErrUnsupported)
	}
	// One byte past the cap is read so a page exactly at the limit is not called truncated.
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxPageTextBytes+1))
	if err != nil {
		return core.FetchedPage{}, translateFetchError(target, err)
	}
	isTruncated := len(body) > MaxPageTextBytes
	if isTruncated {
		body = body[:MaxPageTextBytes]
	}
	text, isTextTruncated := extractReadableText(string(body), MaxPageTextBytes)
	return core.FetchedPage{
		URL: response.Request.URL.String(), ContentType: contentType, Text: text,
		IsTruncated: isTruncated || isTextTruncated, StatusCode: response.StatusCode,
	}, nil
}

// buildFetchClient is the §9.3 client: it dials only addresses it has checked, keeps TLS
// verification against the hostname, and re-checks every redirect.
//
// dialFetchAddress and fetchTLSConfig are fields rather than constants for one reason: a test
// server runs on loopback with a certificate nobody trusts, so those two rules make an
// end-to-end test impossible to write. They default to the real ones, and the address rule
// keeps its own test (reportUnreachableAddressReason), so a test that relaxes them still
// exercises the host allowlist, the redirect checks and the caps for real.
func (b *PolicyEnforcedBroker) buildFetchClient() *http.Client {
	dial := b.dialFetchAddress
	if dial == nil {
		dial = dialCheckedAddress
	}
	transport := &http.Transport{
		// Dialing the resolved address rather than the name is the DNS-rebinding defence: the
		// address that was checked is the address connected to, with no second lookup in
		// between. The request URL still carries the hostname, so the TLS handshake sends it
		// as SNI and verifies the certificate against it (§9.3 "verify SNI").
		DialContext:           dial,
		TLSClientConfig:       b.fetchTLSConfig,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   fetchTimeout / 2,
		ResponseHeaderTimeout: fetchTimeout / 2,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   fetchTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirectHops {
				return fmt.Errorf("more than %d redirects: %w", maxRedirectHops, core.ErrDenied)
			}
			return b.authorizeRedirectHop(request)
		},
	}
}

// authorizeRedirectHop re-validates a hop the first request never named (§9.3). A hop must be
// allowed outright: a host that would merely ask is refused here rather than interrupting a
// redirect chain with a question about a URL the user never saw. The model is told the hop was
// refused and can fetch that address directly, which asks properly.
func (b *PolicyEnforcedBroker) authorizeRedirectHop(request *http.Request) error {
	hop, err := validateFetchURL(request.URL.String())
	if err != nil {
		return err
	}
	decision := b.sessionPolicy.Evaluate(request.Context(), buildPolicyRequest(request.Context(), core.CapabilityNetworkHTTP, hop.Hostname()))
	if decision.Verdict != policy.Allow {
		denied := core.Capability{Kind: core.CapabilityNetworkHTTP, Scope: hop.Hostname()}
		rule := reportDecidingRule(decision)
		_ = b.appendBrokerFact(request.Context(), core.SessionEntry{Kind: core.EntryPolicyDenied, Capability: &denied, Detail: rule})
		return fmt.Errorf("redirect to %q is not permitted (%s): %w", hop.Hostname(), rule, core.ErrDenied)
	}
	return nil
}

// dialCheckedAddress resolves the host once and connects to an address that passed the §9.3
// address rules, so a name that resolves to a private address cannot be reached through fetch.
func dialCheckedAddress(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%q is not a dialable address: %w", address, core.ErrInvalid)
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("%q could not be resolved: %w", host, err)
	}
	dialer := &net.Dialer{Timeout: fetchTimeout / 2}
	var lastErr error
	for _, candidate := range resolved {
		if reason := reportUnreachableAddressReason(candidate.IP); reason != "" {
			// Every address is reported, so a name resolving only to private space is refused
			// rather than silently failing to connect.
			lastErr = fmt.Errorf("%q resolves to %s, which is %s: %w", host, candidate.IP, reason, core.ErrDenied)
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%q resolved to no addresses: %w", host, core.ErrNotFound)
	}
	return nil, lastErr
}

// reportUnreachableAddressReason names why an address may not be fetched, or "" if it may.
// Private, loopback and link-local space is where a machine's own services live — including
// the cloud metadata address on 169.254.169.254, which hands out credentials to anything that
// asks (§9.3).
func reportUnreachableAddressReason(ip net.IP) string {
	switch {
	case ip == nil:
		return "not an address"
	case ip.IsLoopback():
		return "this machine"
	case ip.IsPrivate():
		return "a private network"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return "link-local, where cloud metadata services live"
	case ip.IsUnspecified():
		return "unspecified"
	case ip.IsMulticast(), ip.IsInterfaceLocalMulticast():
		return "a multicast address"
	case isSharedAddressSpace(ip):
		return "carrier-internal space"
	}
	return ""
}

// isSharedAddressSpace reports RFC 6598 100.64.0.0/10, which net.IP.IsPrivate does not cover
// and which reaches carrier-internal hosts.
func isSharedAddressSpace(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

// translateFetchError keeps a refusal a refusal: http.Client wraps whatever the dialer or the
// redirect check returned in a *url.Error, so the core kind has to be rescued from inside it.
func translateFetchError(target *url.URL, err error) error {
	return fmt.Errorf("%q could not be fetched: %w", target.Redacted(), err)
}

func isReadableContentType(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	switch {
	case mediaType == "", strings.HasPrefix(mediaType, "text/"):
		return true
	case strings.HasSuffix(mediaType, "+json"), strings.HasSuffix(mediaType, "+xml"):
		return true
	}
	return mediaType == "application/json" || mediaType == "application/xml" || mediaType == "application/xhtml+xml"
}

// extractReadableText reduces a page to the words a reader would see: script and style bodies
// are dropped entirely, tags are removed, entities are decoded, and runs of blank space
// collapse. It reports whether it had to stop at the cap.
//
// ponytail: a scanner, not an HTML parser — enough for "what does this page say", and it adds
// no dependency. §9.3's fuller "sanitised to markdown" (headings, links, lists preserved)
// wants golang.org/x/net/html; swap this for it when the model needs the structure.
func extractReadableText(document string, maxBytes int) (string, bool) {
	var text strings.Builder
	isTruncated := false
	for remaining := document; remaining != ""; {
		before, after, isTagOpen := strings.Cut(remaining, "<")
		text.WriteString(before)
		if !isTagOpen {
			break
		}
		if skipped, isSkippable := skipElementBody(after); isSkippable {
			remaining = skipped
			text.WriteString(" ")
			continue
		}
		_, afterTag, isTagClosed := strings.Cut(after, ">")
		if !isTagClosed {
			break
		}
		// A tag is a word boundary: "<p>one<p>two" must not read as "onetwo".
		text.WriteString(" ")
		remaining = afterTag
	}
	collapsed := strings.Join(strings.Fields(html.UnescapeString(text.String())), " ")
	if len(collapsed) > maxBytes {
		collapsed, isTruncated = collapsed[:maxBytes], true
	}
	return collapsed, isTruncated
}

// skipElementBody drops a script, style or comment whole, rather than leaving its contents to
// read as page text.
func skipElementBody(afterOpeningBracket string) (string, bool) {
	if comment, isComment := strings.CutPrefix(afterOpeningBracket, "!--"); isComment {
		_, after, _ := strings.Cut(comment, "-->")
		return after, true
	}
	lowered := strings.ToLower(afterOpeningBracket)
	for _, name := range []string{"script", "style"} {
		if !strings.HasPrefix(lowered, name) {
			continue
		}
		// Only an element, not a tag that merely starts with the same letters.
		rest := afterOpeningBracket[len(name):]
		if rest != "" && !strings.ContainsAny(rest[:1], " \t\r\n>/") {
			continue
		}
		if _, after, isClosed := cutFold(afterOpeningBracket, "</"+name); isClosed {
			_, afterClose, _ := strings.Cut(after, ">")
			return afterClose, true
		}
		return "", true
	}
	return "", false
}

// cutFold is strings.Cut with a case-insensitive separator, for tag names a page may spell
// in any case.
func cutFold(s, separator string) (before, after string, isFound bool) {
	index := strings.Index(strings.ToLower(s), strings.ToLower(separator))
	if index < 0 {
		return s, "", false
	}
	return s[:index], s[index+len(separator):], true
}
