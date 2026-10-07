package cassette

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testToken = "sk-test-secret-value"

func TestCassetteReplaysWhatWasRecordedWithoutTheNetwork(t *testing.T) {
	const reply = "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, reply)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "cassettes", "round-trip.jsonl")

	send := func(t *testing.T, client *http.Client) string {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+testToken)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("got status %d, %v", response.StatusCode, err)
		}
		return string(body)
	}

	t.Run("record", func(t *testing.T) {
		*recordFlag = true
		defer func() { *recordFlag = false }()
		if body := send(t, BuildClient(t, path)); body != reply {
			t.Fatalf("recording passed through %q, want %q", body, reply)
		}
	})
	server.Close()
	t.Run("replay", func(t *testing.T) {
		if body := send(t, BuildClient(t, path)); body != reply {
			t.Fatalf("replay gave %q, want %q", body, reply)
		}
	})

	recorded, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(recorded), testToken) || !strings.Contains(string(recorded), `"request":{"model":"m"}`) {
		t.Fatalf("cassette is %q (%v), want the request body and no credential", recorded, err)
	}
}

func TestRecorderRefusesToSaveABearerToken(t *testing.T) {
	recorder := &recorder{
		exchanges: []exchange{{Request: json.RawMessage(`{}`), Status: http.StatusOK, Body: "echoed " + testToken}},
		tokens:    []string{testToken},
	}
	path := filepath.Join(t.TempDir(), "leak.jsonl")

	if err := recorder.save(path); err == nil {
		t.Fatal("saved a recording that contains the bearer token")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat = %v, want no file written", err)
	}
}
