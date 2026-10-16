package core

import "context"

// PageFetcher reads one web page on a tool's behalf (D-04, §9.3). It is a capability of its own
// rather than something a tool reaches by running curl: network access and program execution are
// different powers, and granting exec to fetch a URL would hand over far more than reading a page.
//
// Like CommandRunner it is a port of its own, because only a policy-enforcing broker may reach
// the network at all. The fetcher owns every protection: HTTPS only, the host allowlist, the
// refusal to reach private addresses, the redirect rules and the size and time caps.
type PageFetcher interface {
	FetchPage(ctx context.Context, url string) (FetchedPage, error)
}

// FetchedPage is a page that was actually retrieved. Text is the page's own content and is
// never trusted: it is wrapped with WrapUntrustedContent before the model sees it, and the step
// that received it is marked, so the next effect asks (§5.12.4).
type FetchedPage struct {
	// URL is where the content finally came from, which is not the requested URL when the
	// server redirected; the model is told, because what it read is what that page said.
	URL         string
	ContentType string
	// Text is the page sanitised to plain text, with scripts and styles removed (§9.3).
	Text string
	// IsTruncated reports that the page was longer than the fetcher's cap and the rest was
	// dropped; what is here is still the start of the real page.
	IsTruncated bool
	// StatusCode is the final response's HTTP status. A 404 is a result the model reads, not
	// an error: the page it asked for genuinely says that.
	StatusCode int
}
