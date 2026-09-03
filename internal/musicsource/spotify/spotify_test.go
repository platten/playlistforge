package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"playlistforge/internal/musicsource"
)

func newTestProvider(t *testing.T, mux *http.ServeMux) *Provider {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := New()
	p.apiBase = srv.URL
	return p
}

func session(accessToken, userID string) musicsource.Session {
	raw, _ := json.Marshal(token{AccessToken: accessToken, UserID: userID})
	return musicsource.Session{Kind: musicsource.KindSpotify, Raw: raw}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestAuthRequest(t *testing.T) {
	req, err := New().AuthRequest()
	if err != nil {
		t.Fatalf("AuthRequest: %v", err)
	}
	if req.URL != loginURL || req.ExtractJS == "" || req.RedirectPrefix != "" {
		t.Fatalf("unexpected AuthRequest: %+v", req)
	}
	if req.Width < 1024 {
		t.Fatalf("window too narrow: %d", req.Width)
	}
}

func TestCompleteFromTokenBlob(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-1" {
			t.Fatalf("auth header = %q", r.Header.Get("Authorization"))
		}
		writeJSON(w, map[string]any{"id": "jane", "display_name": "Jane Q"})
	})
	p := newTestProvider(t, mux)

	exp := time.Now().Add(time.Hour).UnixMilli()
	captured := `{"accessToken":"access-1","accessTokenExpirationTimestampMs":` + strconv.FormatInt(exp, 10) + `}`
	s, err := p.Complete(context.Background(), captured)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if s.DisplayName != "Jane Q" || s.Kind != musicsource.KindSpotify {
		t.Fatalf("session = %+v", s)
	}
	if s.ExpiresAt.Before(time.Now().Add(50 * time.Minute)) {
		t.Fatalf("expiry not taken from blob: %v", s.ExpiresAt)
	}
	var tok token
	if err := json.Unmarshal(s.Raw, &tok); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if tok.AccessToken != "access-1" || tok.UserID != "jane" {
		t.Fatalf("token = %+v", tok)
	}
}

func TestCompleteFromBareToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"id": "bob"})
	})
	p := newTestProvider(t, mux)

	s, err := p.Complete(context.Background(), `"access-2"`)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if s.DisplayName != "bob" || s.ExpiresAt.IsZero() {
		t.Fatalf("session = %+v", s)
	}
}

func TestCompleteRejectsEmptyAndBadToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	p := newTestProvider(t, mux)

	if _, err := p.Complete(context.Background(), "   "); err == nil {
		t.Fatal("expected an error for an empty capture")
	}
	if _, err := p.Complete(context.Background(), `{"accessToken":"stale"}`); err == nil {
		t.Fatal("expected an error when /me rejects the token as unauthorized")
	}
}

func TestCompleteToleratesRateLimitOnVerify(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	p := newTestProvider(t, mux)

	s, err := p.Complete(context.Background(), `{"accessToken":"good"}`)
	if err != nil {
		t.Fatalf("a 429 on the cosmetic /me call must not fail sign-in: %v", err)
	}
	if s.DisplayName == "" || s.ExpiresAt.IsZero() {
		t.Fatalf("session = %+v", s)
	}
	var tok token
	_ = json.Unmarshal(s.Raw, &tok)
	if tok.AccessToken != "good" {
		t.Fatalf("token not stored: %+v", tok)
	}
}

func TestRefreshReportsDisconnected(t *testing.T) {
	_, err := New().Refresh(context.Background(), session("x", "y"))
	if !errors.Is(err, musicsource.ErrNotConnected) {
		t.Fatalf("Refresh err = %v, want ErrNotConnected", err)
	}
}

func TestListPlaylists(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/me/playlists", func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		if offset == "0" {
			items := make([]map[string]any, playlistPageSize)
			for i := range items {
				items[i] = map[string]any{"id": "p" + strconv.Itoa(i), "name": "Filler", "snapshot_id": "s" + strconv.Itoa(i)}
			}
			items[0] = map[string]any{
				"id": "focus", "name": "Deep Focus", "description": "quiet",
				"snapshot_id": "snap-1", "tracks": map[string]any{"total": 30},
				"external_urls": map[string]any{"spotify": "https://open.spotify.com/playlist/focus"},
			}
			writeJSON(w, map[string]any{"total": playlistPageSize + 1, "items": items})
			return
		}
		writeJSON(w, map[string]any{
			"total": playlistPageSize + 1,
			"items": []map[string]any{{"id": "tail", "name": "Tail", "snapshot_id": "snap-t"}},
		})
	})
	p := newTestProvider(t, mux)

	pls, err := p.ListPlaylists(context.Background(), session("tok", "me"))
	if err != nil {
		t.Fatalf("ListPlaylists: %v", err)
	}
	if len(pls) != playlistPageSize+1 {
		t.Fatalf("got %d playlists", len(pls))
	}
	if pls[0].ExternalID != "focus" || pls[0].TrackCount != 30 || pls[0].ETag != "snap-1" ||
		pls[0].URL != "https://open.spotify.com/playlist/focus" {
		t.Fatalf("first playlist = %+v", pls[0])
	}
	if pls[len(pls)-1].ExternalID != "tail" {
		t.Fatalf("last playlist = %+v", pls[len(pls)-1])
	}
}

func TestPlaylistTracks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/playlists/focus/tracks", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") == "" {
			t.Fatal("missing fields mask")
		}
		writeJSON(w, map[string]any{
			"total": 3,
			"items": []map[string]any{
				{"track": map[string]any{
					"id": "t1", "name": "One", "is_local": false,
					"artists":      []map[string]any{{"name": "A"}, {"name": "B"}},
					"album":        map[string]any{"name": "Album", "release_date": "2019-05-01"},
					"external_ids": map[string]any{"isrc": "US1234500001"},
				}},
				{"track": map[string]any{"id": "t2", "name": "Local", "is_local": true}},
				{"track": nil},
			},
		})
	})
	p := newTestProvider(t, mux)

	tracks, err := p.PlaylistTracks(context.Background(), session("tok", "me"), "focus")
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks (local + null should be skipped)", len(tracks))
	}
	tr := tracks[0]
	if tr.ISRC == nil || *tr.ISRC != "US1234500001" {
		t.Fatalf("ISRC = %v", tr.ISRC)
	}
	if len(tr.Artists) != 2 || tr.Artists[1] != "B" || tr.Position != 1 {
		t.Fatalf("track = %+v", tr)
	}
	if tr.ReleaseYear == nil || *tr.ReleaseYear != 2019 {
		t.Fatalf("release year = %v", tr.ReleaseYear)
	}
}

func TestUnauthorizedMapsToErrNotConnected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/me/playlists", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	p := newTestProvider(t, mux)

	_, err := p.ListPlaylists(context.Background(), session("expired", "me"))
	if !errors.Is(err, musicsource.ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}

func TestRateLimitRetry(t *testing.T) {
	var calls int
	mux := http.NewServeMux()
	mux.HandleFunc("/me/playlists", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeJSON(w, map[string]any{"total": 0, "items": []any{}})
	})
	p := newTestProvider(t, mux)

	if _, err := p.ListPlaylists(context.Background(), session("tok", "me")); err != nil {
		t.Fatalf("ListPlaylists: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected one retry, got %d calls", calls)
	}
}

func TestDecodeRejectsEmptySession(t *testing.T) {
	if _, err := decode(musicsource.Session{Raw: json.RawMessage(`{}`)}); !errors.Is(err, musicsource.ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}
