package deezer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"playlistforge/internal/playlist"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLookupMatchesRecording(t *testing.T) {
	isrc, version := "USQX91300108", "Original Mix"
	response := `{"data":[
 {"title":"Get Lucky","title_short":"Get Lucky","title_version":"Original Mix","isrc":"WRONG","readable":true,"preview":"https://cdnt-preview.dzcdn.net/wrong.mp3","artist":{"name":"Daft Punk"}},
 {"title":"Get Lucky","title_short":"Get Lucky","title_version":"Original Mix","isrc":"USQX91300108","readable":true,"preview":"https://evil.example/clip.mp3","artist":{"name":"Daft Punk"}},
 {"title":"Get Lucky","title_short":"Get Lucky","title_version":"Radio Edit","isrc":"USQX91300108","readable":true,"preview":"https://cdnt-preview.dzcdn.net/edit.mp3","artist":{"name":"Daft Punk"}},
 {"title":"Get Lucky","title_short":"Get Lucky","title_version":"Original Mix","isrc":"USQX91300108","readable":true,"preview":"https://cdnt-preview.dzcdn.net/ok.mp3","artist":{"name":"Daft Punk"}}]}`
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.deezer.com" || r.URL.Path != "/search" || r.URL.Query().Get("q") != "Get Lucky Daft Punk" || r.URL.Query().Get("limit") != "25" {
			t.Fatalf("request: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: http.Header{}}, nil
	})}
	preview, err := lookup(context.Background(), client, playlist.Track{Title: "Get Lucky", Artists: []string{"Daft Punk"}, ISRC: &isrc, Version: &version})
	if err != nil || preview.URL != "https://cdnt-preview.dzcdn.net/ok.mp3" {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
}

func TestLookupPrefersMatchingAlbum(t *testing.T) {
	body := `{"data":[{"title":"Get Lucky (Radio Edit)","title_short":"Get Lucky","readable":true,"preview":"https://cdnt-preview.dzcdn.net/edit.mp3","artist":{"name":"Daft Punk"},"album":{"title":"Get Lucky"}},{"title":"Get Lucky (feat. Pharrell)","title_short":"Get Lucky","readable":true,"preview":"https://cdnt-preview.dzcdn.net/album.mp3","artist":{"name":"Daft Punk"},"album":{"title":"Random Access Memories"}}]}`
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	preview, err := lookup(context.Background(), client, playlist.Track{Title: "Get Lucky", Artists: []string{"Daft Punk"}, Album: "Random Access Memories"})
	if err != nil || !strings.HasSuffix(preview.URL, "album.mp3") {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
}

func TestLookupUnavailableAndProviderErrors(t *testing.T) {
	track := playlist.Track{Title: "Song", Artists: []string{"Artist"}}
	for _, tc := range []struct {
		name, body string
		status     int
		noPreview  bool
	}{
		{"no match", `{"data":[{"title":"Another Song","title_short":"Another Song","readable":true,"preview":"https://cdnt-preview.dzcdn.net/a.mp3","artist":{"name":"Artist"}}]}`, 200, true},
		{"provider error", `{"error":{"message":"rate limited"}}`, 200, false},
		{"HTTP error", `{}`, 429, false},
		{"bad JSON", `{`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{}}, nil
			})}
			_, err := lookup(context.Background(), client, track)
			if err == nil || errors.Is(err, ErrNoPreview) != tc.noPreview {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
