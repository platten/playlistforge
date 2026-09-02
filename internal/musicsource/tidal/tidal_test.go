package tidal

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"playlistforge/internal/musicsource"
)

// newTestProvider points a Provider at a stub server that stands in for both
// auth.tidal.com and api.tidal.com.
func newTestProvider(t *testing.T, handler http.Handler) (*Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New()
	p.loginBase = "https://login.tidal.com"
	p.authBase = srv.URL
	p.apiBase = srv.URL
	return p, srv
}

func TestAuthRequestPKCE(t *testing.T) {
	p := New()
	req, err := p.AuthRequest()
	if err != nil {
		t.Fatalf("AuthRequest: %v", err)
	}
	if req.RedirectPrefix != redirectURI {
		t.Fatalf("redirect prefix = %q, want %q", req.RedirectPrefix, redirectURI)
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" {
		t.Fatalf("challenge method = %q", q.Get("code_challenge_method"))
	}
	if q.Get("client_id") != clientID || q.Get("redirect_uri") != redirectURI {
		t.Fatalf("unexpected client_id/redirect_uri: %v", q)
	}

	// The stashed verifier must hash to the challenge in the URL.
	if p.pendingVerifier == "" {
		t.Fatal("verifier was not stashed")
	}
	sum := sha256.Sum256([]byte(p.pendingVerifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if q.Get("code_challenge") != want {
		t.Fatalf("challenge = %q, want %q", q.Get("code_challenge"), want)
	}
}

func TestCompleteListAndTracks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.PostForm.Get("grant_type") != "authorization_code" {
			t.Fatalf("grant_type = %q", r.PostForm.Get("grant_type"))
		}
		if r.PostForm.Get("code_verifier") == "" || r.PostForm.Get("code") != "the-code" {
			t.Fatalf("missing code/verifier: %v", r.PostForm)
		}
		writeJSON(w, map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"expires_in":    3600,
			"user": map[string]any{
				"userId":      49927020,
				"countryCode": "NO",
				"username":    "listener@example.com",
			},
		})
	})
	mux.HandleFunc("/users/49927020/playlists", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("auth header = %q", got)
		}
		if r.URL.Query().Get("countryCode") != "NO" {
			t.Fatalf("countryCode = %q", r.URL.Query().Get("countryCode"))
		}
		offset := r.URL.Query().Get("offset")
		if offset == "0" {
			items := make([]map[string]any, pageSize)
			for i := range items {
				items[i] = map[string]any{"uuid": "u" + strconv.Itoa(i), "title": "P" + strconv.Itoa(i), "numberOfTracks": 1}
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
	p, _ := newTestProvider(t, mux)

	if _, err := p.AuthRequest(); err != nil {
		t.Fatalf("AuthRequest: %v", err)
	}
	session, err := p.Complete(context.Background(), redirectURI+"?code=the-code&state=xyz")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if session.DisplayName != "listener@example.com" || session.ExpiresAt.IsZero() {
		t.Fatalf("unexpected session: %+v", session)
	}
	var tok token
	if err := json.Unmarshal(session.Raw, &tok); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if tok.AccessToken != "access-1" || tok.UserID != "49927020" || tok.CountryCode != "NO" {
		t.Fatalf("token = %+v", tok)
	}

	playlists, err := p.ListPlaylists(context.Background(), session)
	if err != nil {
		t.Fatalf("ListPlaylists: %v", err)
	}
	if len(playlists) != pageSize+1 {
		t.Fatalf("got %d playlists, want %d", len(playlists), pageSize+1)
	}
	if playlists[0].ExternalID != "uuid-a" || playlists[0].TrackCount != 2 {
		t.Fatalf("first playlist = %+v", playlists[0])
	}
	if playlists[0].URL != "https://tidal.com/playlist/uuid-a" || playlists[0].UpdatedAt.IsZero() {
		t.Fatalf("first playlist url/time = %+v", playlists[0])
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

func TestCompleteErrors(t *testing.T) {
	p, _ := newTestProvider(t, http.NotFoundHandler())

	if _, err := p.AuthRequest(); err != nil {
		t.Fatalf("AuthRequest: %v", err)
	}
	if _, err := p.Complete(context.Background(), redirectURI+"?error=access_denied"); err == nil {
		t.Fatal("expected error for denied sign-in")
	}

	if _, err := p.AuthRequest(); err != nil {
		t.Fatalf("AuthRequest: %v", err)
	}
	if _, err := p.Complete(context.Background(), redirectURI+"?state=nope"); err == nil {
		t.Fatal("expected error for missing code")
	}

	// No AuthRequest first -> no verifier.
	if _, err := p.Complete(context.Background(), redirectURI+"?code=x"); err == nil {
		t.Fatal("expected error with no sign-in in progress")
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
	p, _ := newTestProvider(t, mux)

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
	p, _ := newTestProvider(t, mux)

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
