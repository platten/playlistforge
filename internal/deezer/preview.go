// Package deezer looks up short public track previews without an account.
package deezer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"playlistforge/internal/playlist"
)

var ErrNoPreview = errors.New("no matching Deezer preview is available")

// Preview is the matching recording and the short clip Deezer makes public.
type Preview struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
}

type searchResult struct {
	Data []struct {
		Title        string `json:"title"`
		TitleShort   string `json:"title_short"`
		TitleVersion string `json:"title_version"`
		ISRC         string `json:"isrc"`
		Preview      string `json:"preview"`
		Album        struct {
			Title string `json:"title"`
		} `json:"album"`
		Readable bool `json:"readable"`
		Artist   struct {
			Name string `json:"name"`
		} `json:"artist"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Lookup searches Deezer's catalog and returns only a matching recording.
func Lookup(ctx context.Context, track playlist.Track) (Preview, error) {
	return lookup(ctx, &http.Client{Timeout: 10 * time.Second}, track)
}

func lookup(ctx context.Context, client *http.Client, track playlist.Track) (Preview, error) {
	if len(track.Title) == 0 || len(track.Title) > 300 || len(track.Artists) == 0 || len(track.Artists[0]) == 0 || len(track.Artists[0]) > 300 {
		return Preview{}, ErrNoPreview
	}
	query := url.Values{"q": {track.Title + " " + track.Artists[0]}, "limit": {"25"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.deezer.com/search?"+query.Encode(), nil)
	if err != nil {
		return Preview{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Preview{}, fmt.Errorf("search Deezer: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Preview{}, fmt.Errorf("Deezer search returned HTTP %d", response.StatusCode)
	}
	var result searchResult
	if err := json.NewDecoder(io.LimitReader(response.Body, 512*1024)).Decode(&result); err != nil {
		return Preview{}, fmt.Errorf("decode Deezer search: %w", err)
	}
	if result.Error != nil {
		return Preview{}, fmt.Errorf("Deezer search: %s", result.Error.Message)
	}
	best := Preview{}
	bestScore := -1
	for _, candidate := range result.Data {
		if !candidate.Readable || !previewURLAllowed(candidate.Preview) || normalize(candidate.Artist.Name) != normalize(track.Artists[0]) {
			continue
		}
		title := normalize(track.Title)
		if title != normalize(candidate.Title) && title != normalize(candidate.TitleShort) {
			continue
		}
		if track.ISRC != nil && *track.ISRC != "" && !strings.EqualFold(*track.ISRC, candidate.ISRC) {
			continue
		}
		if track.Version != nil && *track.Version != "" && normalize(*track.Version) != normalize(candidate.TitleVersion) {
			continue
		}
		score := 0
		if title == normalize(candidate.Title) {
			score++
		}
		if track.Album != "" && normalize(track.Album) == normalize(candidate.Album.Title) {
			score += 2
		}
		if score > bestScore {
			best = Preview{URL: candidate.Preview, Title: candidate.Title, Artist: candidate.Artist.Name}
			bestScore = score
		}
	}
	if bestScore >= 0 {
		return best, nil
	}
	return Preview{}, ErrNoPreview
}

func previewURLAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "dzcdn.net" || strings.HasSuffix(host, ".dzcdn.net")
}

func normalize(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
