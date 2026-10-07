// Package cassette records a real model's HTTP traffic once and replays it in tests (D-16), so
// provider and loop tests run offline, fast, and against real model output.
package cassette

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// recordFlag switches every cassette client in the test binary from replaying to recording
// against a live endpoint: go test ./cmd/travis -run Recorded -record-cassettes
var recordFlag = flag.Bool("record-cassettes", false, "record cassettes against a live model instead of replaying them")

// exchange is one line of a cassette: one model call. The request is kept for reviewers to read
// what the model was shown; replay does not compare it.
type exchange struct {
	Request json.RawMessage `json:"request"`
	Status  int             `json:"status"`
	Body    string          `json:"body"`
}

// IsRecording reports whether tests should record against a live endpoint instead of replaying.
func IsRecording() bool {
	return *recordFlag
}

// BuildClient returns a client that replays the cassette at path, or records it there under
// -record-cassettes. Replay never touches the network: a missing cassette, a request
// with no recording left, or a recording never requested all fail the test.
func BuildClient(t testing.TB, path string) *http.Client {
	t.Helper()
	if IsRecording() {
		recorder := &recorder{upstream: http.DefaultTransport}
		t.Cleanup(func() {
			if t.Failed() {
				return
			}
			if err := recorder.save(path); err != nil {
				t.Error(err)
			}
		})
		return &http.Client{Transport: recorder}
	}

	exchanges, err := loadExchanges(path)
	if err != nil {
		t.Fatalf("%v (record it with -record-cassettes against a real model)", err)
	}
	player := &player{t: t, path: path, exchanges: exchanges}
	t.Cleanup(player.requireAllPlayed)
	return &http.Client{Transport: player}
}

// recorder forwards each request to the real endpoint and keeps a copy of the exchange.
type recorder struct {
	upstream http.RoundTripper

	mu        sync.Mutex
	exchanges []exchange
	tokens    []string
}

func (r *recorder) RoundTrip(request *http.Request) (*http.Response, error) {
	var requestBody []byte
	if request.Body != nil {
		var err error
		requestBody, err = io.ReadAll(request.Body)
		_ = request.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	forwarded := request.Clone(request.Context())
	forwarded.Body = io.NopCloser(bytes.NewReader(requestBody))

	response, err := r.upstream.RoundTrip(forwarded)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.exchanges = append(r.exchanges, exchange{Request: requestBody, Status: response.StatusCode, Body: string(responseBody)})
	if token, hasToken := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer "); hasToken && token != "" {
		r.tokens = append(r.tokens, token)
	}
	r.mu.Unlock()

	response.Body = io.NopCloser(bytes.NewReader(responseBody))
	return response, nil
}

// save writes one exchange per line. Headers and URLs are never recorded, and a recording
// that still contains a bearer token it saw (echoed in a body, say) is refused outright.
func (r *recorder) save(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	for _, recorded := range r.exchanges {
		if err := encoder.Encode(recorded); err != nil {
			return fmt.Errorf("cassette %s: %w", path, err)
		}
	}
	for _, token := range r.tokens {
		if bytes.Contains(encoded.Bytes(), []byte(token)) {
			return fmt.Errorf("cassette %s: recording contains a bearer token, not writing it", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, encoded.Bytes(), 0o600)
}

func loadExchanges(path string) ([]exchange, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cassette: %w", err)
	}
	defer func() { _ = file.Close() }()

	var exchanges []exchange
	decoder := json.NewDecoder(file)
	for {
		var recorded exchange
		err := decoder.Decode(&recorded)
		if errors.Is(err, io.EOF) {
			return exchanges, nil
		}
		if err != nil {
			return nil, fmt.Errorf("cassette %s: line %d: %w", path, len(exchanges)+1, err)
		}
		exchanges = append(exchanges, recorded)
	}
}

// player hands out recorded responses in order.
// ponytail: matches by order, not request body; compare bodies if a cassette silently drifts.
type player struct {
	t    testing.TB
	path string

	mu        sync.Mutex
	exchanges []exchange
}

func (p *player) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		_ = request.Body.Close()
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.exchanges) == 0 {
		err := fmt.Errorf("cassette %s: no recorded response left for %s %s", p.path, request.Method, request.URL.Path)
		p.t.Error(err)
		return nil, err
	}
	next := p.exchanges[0]
	p.exchanges = p.exchanges[1:]
	return &http.Response{
		Status:     fmt.Sprintf("%d %s", next.Status, http.StatusText(next.Status)),
		StatusCode: next.Status,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(next.Body)),
		Request:    request,
	}, nil
}

func (p *player) requireAllPlayed() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.exchanges) > 0 {
		p.t.Errorf("cassette %s: %d recorded responses were never requested", p.path, len(p.exchanges))
	}
}
