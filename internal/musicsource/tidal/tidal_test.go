package tidal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"playlistforge/internal/musicsource"
)

// newTestProvider points a Provider at a stub server that stands in for both
// auth.tidal.com and api.tidal.com, with a fast poll interval.
func newTestProvider(t *testing.T, handler http.Handler) *Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New()
	p.authBase = srv.URL
	p.apiBase = srv.URL
	p.minPollInterval = time.Millisecond
	return p
}

func TestAuthRequestStartsDeviceFlow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth2/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("client_id") != clientID || r.PostForm.Get("scope") != scope {
			t.Fatalf("unexpected device-auth form: %v", r.PostForm)
		}
		writeJSON(w, map[string]any{
			"deviceCode":              "dev-1",
			"userCode":                "ABCD-EFGH",
			"verificationUri":         "link.tidal.com",
			"verificationUriComplete": "link.tidal.com/ABCDEFGH",
			"expiresIn":               300,
			"interval":                0.001,
		})
	})
	p := newTestProvider(t, mux)

	req, err := p.AuthRequest()
	if err != nil {
		t.Fatalf("AuthRequest: %v", err)
	}
	if !req.OpenInBrowser {
		t.Fatal("device flow must open in the system browser")
	}
	if req.URL != "https://link.tidal.com/ABCDEFGH" {
		t.Fatalf("verification URL = %q", req.URL)
	}
	p.mu.Lock()
	pending := p.pending
	p.mu.Unlock()
	if pending.deviceCode != "dev-1" || pending.expires.IsZero() {
		t.Fatalf("pending device not stored: %+v", pending)
	}
}

func TestCompletePollsUntilApproved(t *testing.T) {
	var polls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth2/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"deviceCode": "dev-1", "userCode": "X", "verificationUri": "link.tidal.com",
			"verificationUriComplete": "link.tidal.com/X", "expiresIn": 300, "interval": 0.001,
		})
	})
	mux.HandleFunc("/v1/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != deviceCodeGrant || r.PostForm.Get("device_code") != "dev-1" {
			t.Fatalf("unexpected token form: %v", r.PostForm)
		}
		if n := atomic.AddInt32(&polls, 1); n < 3 {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "authorization_pending"})
			return
		}
		writeJSON(w, map[string]any{
			"access_token": "access-1", "refresh_token": "refresh-1", "expires_in": 3600,
		})
	})
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-1" {
			t.Fatalf("sessions auth header = %q", r.Header.Get("Authorization"))
		}
		writeJSON(w, map[string]any{"sessionId": "s1", "userId": 49927020, "countryCode": "NO"})
	})
	p := newTestProvider(t, mux)

	if _, err := p.AuthRequest(); err != nil {
		t.Fatalf("AuthRequest: %v", err)
	}
	session, err := p.Complete(context.Background(), "")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if polls < 3 {
		t.Fatalf("expected polling, got %d requests", polls)
	}
	var tok token
	if err := json.Unmarshal(session.Raw, &tok); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if tok.AccessToken != "access-1" || tok.UserID != "49927020" || tok.CountryCode != "NO" {
		t.Fatalf("token = %+v", tok)
	}
	if session.ExpiresAt.IsZero() {
		t.Fatal("expiry not set")
	}
}

func TestCompleteReportsExpiry(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth2/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"deviceCode": "dev-1", "verificationUriComplete": "link.tidal.com/X",
			"expiresIn": 300, "interval": 0.001,
		})
	})
	mux.HandleFunc("/v1/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "expired_token"})
	})
	p := newTestProvider(t, mux)

	if _, err := p.AuthRequest(); err != nil {
		t.Fatalf("AuthRequest: %v", err)
	}
	if _, err := p.Complete(context.Background(), ""); err == nil {
		t.Fatal("expected an error for an expired device code")
	}
}

func TestCompleteWithoutAuthRequest(t *testing.T) {
	p := newTestProvider(t, http.NotFoundHandler())
	if _, err := p.Complete(context.Background(), ""); err == nil {
		t.Fatal("expected an error with no sign-in in progress")
	}
}

func TestCompleteHonoursContextCancellation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth2/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"deviceCode": "dev-1", "verificationUriComplete": "link.tidal.com/X",
			"expiresIn": 300, "interval": 0.05,
		})
	})
	mux.HandleFunc("/v1/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "authorization_pending"})
	})
	p := newTestProvider(t, mux)
	p.minPollInterval = 50 * time.Millisecond

	if _, err := p.AuthRequest(); err != nil {
		t.Fatalf("AuthRequest: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := p.Complete(ctx, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestListAndTracks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/49927020/playlists", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-1" {
			t.Fatalf("auth header = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("countryCode") != "NO" {
			t.Fatalf("countryCode = %q", r.URL.Query().Get("countryCode"))
		}
		if r.URL.Query().Get("offset") == "0" {
			items := make([]map[string]any, pageSize)
			for i := range items {
				items[i] = map[string]any{"uuid": "u" + strconv.Itoa(i), "title": "P", "numberOfTracks": 1}
			}
			items[0] = map[string]any{
				"uuid": "uuid-a", "title": "Road trip", "description": "for the car",
				"numberOfTracks": 2, "lastUpdated": "2017-01-18T16:31:51.839+0000",
			}
			writeJSON(w, map[string]any{"totalNumberOfItems": pageSize + 1, "items": items})
			return
		}
		writeJSON(w, map[string]any{
			"totalNumberOfItems": pageSize + 1,
			"items":              []map[string]any{{"uuid": "uuid-b", "title": "Encore", "numberOfTracks": 0}},
		})
	})
	mux.HandleFunc("/playlists/uuid-a/tracks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"totalNumberOfItems": 2,
			"items": []map[string]any{
				{
					"id": 1001, "title": "First", "isrc": "USRC17607839",
					"artists": []map[string]any{{"name": "Alice"}, {"name": "Bob"}},
					"album":   map[string]any{"title": "Debut"},
				},
				{
					"id": 1002, "title": "Second", "version": " Remastered ",
					"artist": map[string]any{"name": "Solo"},
					"album":  map[string]any{"title": "Sequel"},
				},
			},
		})
	})
	p := newTestProvider(t, mux)

	raw, _ := json.Marshal(token{AccessToken: "access-1", RefreshToken: "r", UserID: "49927020", CountryCode: "NO"})
	session := musicsource.Session{Kind: musicsource.KindTIDAL, Raw: raw}

	playlists, err := p.ListPlaylists(context.Background(), session)
	if err != nil {
		t.Fatalf("ListPlaylists: %v", err)
	}
	if len(playlists) != pageSize+1 {
		t.Fatalf("got %d playlists", len(playlists))
	}
	if playlists[0].ExternalID != "uuid-a" || playlists[0].TrackCount != 2 ||
		playlists[0].URL != "https://tidal.com/playlist/uuid-a" || playlists[0].UpdatedAt.IsZero() {
		t.Fatalf("first playlist = %+v", playlists[0])
	}
	if playlists[len(playlists)-1].ExternalID != "uuid-b" {
		t.Fatalf("last playlist = %+v", playlists[len(playlists)-1])
	}

	tracks, err := p.PlaylistTracks(context.Background(), session, "uuid-a")
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks", len(tracks))
	}
	if tracks[0].ISRC == nil || *tracks[0].ISRC != "USRC17607839" {
		t.Fatalf("track 0 ISRC = %v", tracks[0].ISRC)
	}
	if len(tracks[0].Artists) != 2 || tracks[0].Artists[1] != "Bob" || tracks[0].Position != 1 {
		t.Fatalf("track 0 = %+v", tracks[0])
	}
	if tracks[1].Version == nil || *tracks[1].Version != "Remastered" {
		t.Fatalf("track 1 version = %v", tracks[1].Version)
	}
	if len(tracks[1].Artists) != 1 || tracks[1].Artists[0] != "Solo" {
		t.Fatalf("track 1 artists = %v", tracks[1].Artists)
	}
}

func TestRefreshKeepsIdentity(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "refresh-1" {
			t.Fatalf("unexpected refresh form: %v", r.PostForm)
		}
		writeJSON(w, map[string]any{"access_token": "access-2", "expires_in": 3600})
	})
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("refresh should not need the sessions endpoint")
	})
	p := newTestProvider(t, mux)

	raw, _ := json.Marshal(token{AccessToken: "access-1", RefreshToken: "refresh-1", UserID: "49927020", CountryCode: "NO"})
	in := musicsource.Session{Kind: musicsource.KindTIDAL, Raw: raw, DisplayName: "listener@example.com"}

	out, err := p.Refresh(context.Background(), in)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if out.DisplayName != "listener@example.com" {
		t.Fatalf("display name lost: %q", out.DisplayName)
	}
	var tok token
	if err := json.Unmarshal(out.Raw, &tok); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if tok.AccessToken != "access-2" || tok.RefreshToken != "refresh-1" || tok.UserID != "49927020" {
		t.Fatalf("refreshed token = %+v", tok)
	}
}

func TestRefreshNoTokenIsNoop(t *testing.T) {
	p := New()
	raw, _ := json.Marshal(token{AccessToken: "a", UserID: "1", CountryCode: "US"})
	in := musicsource.Session{Raw: raw}
	out, err := p.Refresh(context.Background(), in)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if string(out.Raw) != string(raw) {
		t.Fatalf("expected the session unchanged, got %s", out.Raw)
	}
}

func TestUnauthorizedMapsToErrNotConnected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/1/playlists", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	p := newTestProvider(t, mux)

	raw, _ := json.Marshal(token{AccessToken: "expired", UserID: "1", CountryCode: "US"})
	_, err := p.ListPlaylists(context.Background(), musicsource.Session{Raw: raw})
	if !errors.Is(err, musicsource.ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}

func TestDecodeRejectsEmptySession(t *testing.T) {
	if _, err := decode(musicsource.Session{Raw: json.RawMessage(`{}`)}); !errors.Is(err, musicsource.ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
	if _, err := decode(musicsource.Session{Raw: json.RawMessage(`not json`)}); err == nil {
		t.Fatal("expected a decode error")
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
