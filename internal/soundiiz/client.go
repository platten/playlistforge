// Package soundiiz creates temporary public playlist-import handoffs.
package soundiiz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"playlistforge/internal/playlist"
)

// Endpoint is Soundiiz's documented public playlist-import endpoint.
const Endpoint = "https://soundiiz.com/go/import-playlist"

// Client sends accepted playlist metadata to Soundiiz.
type Client struct {
	endpoint string
	http     *http.Client
}

// Result describes a temporary Soundiiz import link.
type Result struct {
	ShareURL  string `json:"shareUrl"`
	ExpiresAt int64  `json:"expiresAt"`
	Tracks    int    `json:"nbTracks"`
}

type importRequest struct {
	Title       string        `json:"title"`
	SourceName  string        `json:"sourceName"`
	Description string        `json:"description,omitempty"`
	Tracklist   []importTrack `json:"tracklist"`
}

type importTrack struct {
	Title   string   `json:"title"`
	Artists []string `json:"artists"`
}

type importResponse struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	ShareURL  string `json:"shareUrl"`
	ExpiresAt int64  `json:"expiresAt"`
	Tracks    int    `json:"nbTracks"`
}

// New returns a production client that refuses redirects. Redirect handling is
// explicit because the response URL crosses a local-to-public trust boundary.
func New() *Client {
	return &Client{endpoint: Endpoint, http: &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// Import submits title/artist metadata and validates the returned Soundiiz URL.
func (c *Client) Import(ctx context.Context, revision playlist.Revision) (Result, error) {
	if strings.TrimSpace(revision.Title) == "" || len(revision.Tracks) < 1 || len(revision.Tracks) > 200 {
		return Result{}, errors.New("playlist must have a title and between 1 and 200 tracks")
	}
	payload := importRequest{Title: revision.Title, SourceName: "Playlist Forge", Description: revision.Description, Tracklist: make([]importTrack, len(revision.Tracks))}
	for i, track := range revision.Tracks {
		payload.Tracklist[i] = importTrack{Title: track.Title, Artists: track.Artists}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, fmt.Errorf("encode Soundiiz request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("create Soundiiz request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("call Soundiiz: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("read Soundiiz response: %w", err)
	}
	var decoded importResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Result{}, fmt.Errorf("decode Soundiiz response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || decoded.Status != "success" {
		message := strings.TrimSpace(decoded.Message)
		if message == "" {
			message = response.Status
		}
		return Result{}, fmt.Errorf("playlist rejected by Soundiiz: %s", message)
	}
	// Never forward an arbitrary URL from a compromised or malformed upstream
	// response to the desktop external-URL handler.
	parsed, err := url.Parse(decoded.ShareURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "soundiiz.com" || parsed.User != nil || !strings.HasPrefix(parsed.Path, "/go/import-playlist/") {
		return Result{}, errors.New("invalid share URL returned by Soundiiz")
	}
	return Result{ShareURL: decoded.ShareURL, ExpiresAt: decoded.ExpiresAt, Tracks: decoded.Tracks}, nil
}
