package qobuz

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"playlistforge/internal/musicsource"
)

const bundleJS = `!function(){var e={production:{api:{appId:"278622936",appSecret:"0123456789abcdef0123456789abcdef"}}};}();`

func newTestProvider(t *testing.T, mux *http.ServeMux, loginHits, bundleHits *int32) (*Provider, *httptest.Server) {
	t.Helper()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if loginHits != nil {
			atomic.AddInt32(loginHits, 1)
		}
		_, _ = w.Write([]byte(`<html><body><script src="/resources/7.1.3-b011/bundle.js" defer></script></body></html>`))
	})
	mux.HandleFunc("/resources/7.1.3-b011/bundle.js", func(w http.ResponseWriter, r *http.Request) {
		if bundleHits != nil {
			atomic.AddInt32(bundleHits, 1)
		}
		_, _ = w.Write([]byte(bundleJS))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := New()
	p.playBase = srv.URL
	p.apiBase = srv.URL + "/api.json/0.2"
	return p, srv
}

func TestAuthRequest(t *testing.T) {
	p := New()
	req, err := p.AuthRequest()
	if err != nil {
		t.Fatalf("AuthRequest: %v", err)
	}
	if req.URL != p.playBase+"/login" {
		t.Fatalf("URL = %q", req.URL)
	}
	if req.RedirectPrefix != "" || req.ExtractJS == "" {
		t.Fatalf("expected an ExtractJS probe, got %+v", req)
	}
}

func TestCompleteScrapesAppIDOnce(t *testing.T) {
	var loginHits, bundleHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api.json/0.2/user/login", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("user_auth_token") != "UAT" || r.URL.Query().Get("app_id") != "278622936" {
			t.Fatalf("unexpected user/login query: %v", r.URL.Query())
		}
		writeJSON(w, map[string]any{"user": map[string]any{"display_name": "Jane Q", "login": "janeq"}})
	})
	p, _ := newTestProvider(t, mux, &loginHits, &bundleHits)

	session, err := p.Complete(context.Background(), `{"id":424242,"token":"UAT"}`)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if session.DisplayName != "Jane Q" || session.Kind != musicsource.KindQobuz {
		t.Fatalf("session = %+v", session)
	}
	var tok token
	if err := json.Unmarshal(session.Raw, &tok); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if tok.AppID != "278622936" || tok.AuthToken != "UAT" || tok.UserID != "424242" {
		t.Fatalf("token = %+v", tok)
	}

	// A second Complete must reuse the cached app id.
	if _, err := p.Complete(context.Background(), `{"id":1,"token":"UAT"}`); err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	if loginHits != 1 || bundleHits != 1 {
		t.Fatalf("app id was re-scraped: login=%d bundle=%d", loginHits, bundleHits)
	}
}

func TestCompleteRejectsBadCapture(t *testing.T) {
	p, _ := newTestProvider(t, http.NewServeMux(), nil, nil)
	if _, err := p.Complete(context.Background(), "not json"); err == nil {
		t.Fatal("expected a parse error")
	}
	if _, err := p.Complete(context.Background(), `{"id":1}`); err == nil {
		t.Fatal("expected an error for a missing token")
	}
}

func TestListPlaylistsFiltersToOwner(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api.json/0.2/user/login", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"user": map[string]any{"login": "owner"}})
	})
	mux.HandleFunc("/api.json/0.2/playlist/getUserPlaylists", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-Auth-Token") != "UAT" {
			t.Fatalf("missing auth token header")
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offset == 0 {
			items := make([]map[string]any, pageSize)
			for i := range items {
				items[i] = map[string]any{
					"id": 5000 + i, "name": "Filler", "tracks_count": 1,
					"owner": map[string]any{"id": 7},
				}
			}
			items[0] = map[string]any{
				"id": 900, "name": "Mine", "description": "kept", "tracks_count": 12,
				"updated_at": 1700000000, "owner": map[string]any{"id": 7},
			}
			items[1] = map[string]any{
				"id": 901, "name": "Someone else's", "tracks_count": 3,
				"owner": map[string]any{"id": 99},
			}
			writeJSON(w, map[string]any{"playlists": map[string]any{"items": items, "total": pageSize + 1}})
			return
		}
		writeJSON(w, map[string]any{"playlists": map[string]any{
			"items": []map[string]any{{"id": 902, "name": "Tail", "owner": map[string]any{"id": 7}}},
			"total": pageSize + 1,
		}})
	})
	p, _ := newTestProvider(t, mux, nil, nil)

	session, err := p.Complete(context.Background(), `{"id":7,"token":"UAT"}`)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	playlists, err := p.ListPlaylists(context.Background(), session)
	if err != nil {
		t.Fatalf("ListPlaylists: %v", err)
	}
	// pageSize owned on page 1 (the "99" one dropped) + 1 on page 2.
	if len(playlists) != pageSize {
		t.Fatalf("got %d playlists, want %d", len(playlists), pageSize)
	}
	if playlists[0].ExternalID != "900" || playlists[0].TrackCount != 12 {
		t.Fatalf("first playlist = %+v", playlists[0])
	}
	if playlists[0].URL != "https://play.qobuz.com/playlist/900" || playlists[0].UpdatedAt.IsZero() {
		t.Fatalf("first playlist url/time = %+v", playlists[0])
	}
	for _, pl := range playlists {
		if pl.ExternalID == "901" {
			t.Fatal("a playlist owned by someone else leaked through")
		}
	}
	if playlists[len(playlists)-1].ExternalID != "902" {
		t.Fatalf("last playlist = %+v", playlists[len(playlists)-1])
	}
}

func TestPlaylistTracks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api.json/0.2/user/login", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"user": map[string]any{"login": "owner"}})
	})
	mux.HandleFunc("/api.json/0.2/playlist/get", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("playlist_id") != "900" || r.URL.Query().Get("extra") != "tracks" {
			t.Fatalf("unexpected query: %v", r.URL.Query())
		}
		writeJSON(w, map[string]any{"tracks": map[string]any{
			"total": 2,
			"items": []map[string]any{
				{
					"id": 11, "title": "Opener", "isrc": "GBAYE0601498", "version": "",
					"performer": map[string]any{"name": "The Band"},
					"album": map[string]any{
						"title":                 "Long Player",
						"artist":                map[string]any{"name": "The Band"},
						"release_date_original": "1971-06-01",
					},
				},
				{
					"id": 12, "title": "Closer",
					"album": map[string]any{
						"title":  "Long Player",
						"artist": map[string]any{"name": "The Band"},
					},
				},
			},
		}})
	})
	p, _ := newTestProvider(t, mux, nil, nil)

	session, err := p.Complete(context.Background(), `{"id":7,"token":"UAT"}`)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	tracks, err := p.PlaylistTracks(context.Background(), session, "900")
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks", len(tracks))
	}
	if tracks[0].ISRC == nil || *tracks[0].ISRC != "GBAYE0601498" {
		t.Fatalf("track 0 ISRC = %v", tracks[0].ISRC)
	}
	if tracks[0].Version != nil {
		t.Fatalf("empty version should be nil, got %v", *tracks[0].Version)
	}
	if tracks[0].ReleaseYear == nil || *tracks[0].ReleaseYear != 1971 {
		t.Fatalf("track 0 release year = %v", tracks[0].ReleaseYear)
	}
	if len(tracks[0].Artists) != 1 || tracks[0].Artists[0] != "The Band" {
		t.Fatalf("track 0 artists = %v", tracks[0].Artists)
	}
	if tracks[1].Position != 2 || tracks[1].Artists[0] != "The Band" {
		t.Fatalf("track 1 = %+v", tracks[1])
	}
}

func TestUnauthorizedMapsToErrNotConnected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api.json/0.2/playlist/getUserPlaylists", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	p, _ := newTestProvider(t, mux, nil, nil)

	raw, _ := json.Marshal(token{AppID: "1", AuthToken: "expired", UserID: "7"})
	_, err := p.ListPlaylists(context.Background(), musicsource.Session{Raw: raw})
	if !errors.Is(err, musicsource.ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}

func TestDecodeRejectsEmptySession(t *testing.T) {
	if _, err := decode(musicsource.Session{Raw: json.RawMessage(`{}`)}); !errors.Is(err, musicsource.ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}

func TestEpochTimeToleratesJunk(t *testing.T) {
	var e epochTime
	for _, in := range []string{`"not-a-time"`, `null`, `0`, `""`} {
		if err := e.UnmarshalJSON([]byte(in)); err != nil {
			t.Fatalf("UnmarshalJSON(%s) errored: %v", in, err)
		}
		if !e.Time.IsZero() {
			t.Fatalf("UnmarshalJSON(%s) set a time: %v", in, e.Time)
		}
	}
	if err := e.UnmarshalJSON([]byte(`1700000000`)); err != nil || e.Time.IsZero() {
		t.Fatalf("valid epoch failed: %v / %v", err, e.Time)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
