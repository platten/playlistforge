package soundiiz

// Tests for the Soundiiz handoff client against httptest servers: a successful
// import, request validation (title and 1-200 tracks), non-2xx and
// non-"success" response bodies, transport and body-read failures, and the
// constructor's refusal to follow redirects. The share URL returned by the
// server is re-validated before it is exposed.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"playlistforge/internal/playlist"
)

func revision() playlist.Revision {
	return playlist.Revision{Title: "Mix", Description: "Desc", Tracks: []playlist.Track{{Title: "One", Artists: []string{"A"}}, {Title: "Two", Artists: []string{"B"}}}}
}

func TestImportSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("bad request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"sourceName":"Playlist Forge"`) || !strings.Contains(string(body), `"artists":["A"]`) {
			t.Errorf("bad body: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","nbTracks":2,"shareUrl":"https://soundiiz.com/go/import-playlist/token","expiresAt":1782220923}`))
	}))
	defer server.Close()
	client := New()
	client.endpoint = server.URL
	result, err := client.Import(context.Background(), revision())
	if err != nil || result.Tracks != 2 || result.ShareURL == "" || result.ExpiresAt == 0 {
		t.Fatalf("result = %#v, %v", result, err)
	}
}

func TestImportValidationAndResponses(t *testing.T) {
	client := New()
	for _, item := range []playlist.Revision{{}, {Title: "x", Tracks: make([]playlist.Track, 201)}} {
		if _, err := client.Import(context.Background(), item); err == nil {
			t.Fatal("expected validation error")
		}
	}
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"rejected", 400, `{"status":"error","message":"bad tracks"}`},
		{"empty error", 500, `{"status":"error"}`},
		{"invalid json", 200, `{`},
		{"wrong status", 200, `{"status":"error","message":"no"}`},
		{"http url", 200, `{"status":"success","shareUrl":"http://soundiiz.com/go/import-playlist/a"}`},
		{"wrong host", 200, `{"status":"success","shareUrl":"https://evil.example/go/import-playlist/a"}`},
		{"port", 200, `{"status":"success","shareUrl":"https://soundiiz.com:444/go/import-playlist/a"}`},
		{"wrong path", 200, `{"status":"success","shareUrl":"https://soundiiz.com/other/a"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			testClient := New()
			testClient.endpoint = server.URL
			if _, err := testClient.Import(context.Background(), revision()); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("broken") }

func TestImportTransportAndReadErrors(t *testing.T) {
	invalid := New()
	invalid.endpoint = "://bad"
	if _, err := invalid.Import(context.Background(), revision()); err == nil || !strings.Contains(err.Error(), "create Soundiiz") {
		t.Fatalf("request error = %v", err)
	}
	client := New()
	client.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") }), Timeout: time.Second}
	if _, err := client.Import(context.Background(), revision()); err == nil || !strings.Contains(err.Error(), "call Soundiiz") {
		t.Fatalf("error = %v", err)
	}
	client.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(brokenReader{}), Header: make(http.Header)}, nil
	})}
	if _, err := client.Import(context.Background(), revision()); err == nil || !strings.Contains(err.Error(), "read Soundiiz") {
		t.Fatalf("error = %v", err)
	}
}

func TestImportClassifiesServiceOutage(t *testing.T) {
	// Transport failure.
	client := New()
	client.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})}
	if _, err := client.Import(context.Background(), revision()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("transport error not classified as unavailable: %v", err)
	}

	// A 503 from Soundiiz.
	for _, status := range []int{429, 500, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		c := New()
		c.endpoint = server.URL
		_, err := c.Import(context.Background(), revision())
		server.Close()
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("status %d not classified as unavailable: %v", status, err)
		}
	}

	// A 400 rejection is NOT an outage.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"status":"error","message":"bad tracks"}`))
	}))
	defer server.Close()
	c := New()
	c.endpoint = server.URL
	if _, err := c.Import(context.Background(), revision()); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("a 400 rejection must not be an outage: %v", err)
	}
}

func TestNewRejectsRedirects(t *testing.T) {
	client := New()
	req, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)
	if err := client.http.CheckRedirect(req, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect = %v", err)
	}
}
