// Package fake is an in-memory musicsource.Provider for tests. It lets the sync
// and merge pipeline be exercised end to end without a network or real
// streaming credentials.
package fake

import (
	"context"
	"encoding/json"
	"fmt"

	"playlistforge/internal/musicsource"
	"playlistforge/internal/playlist"
)

// Provider is a scriptable fake. Set Playlists and Tracks, then use it wherever
// a musicsource.Provider is expected. Call counts are recorded for assertions.
type Provider struct {
	ServiceKind musicsource.Kind
	// Playlists is what ListPlaylists returns. Mutate it between syncs to
	// simulate added, changed (bump ETag/UpdatedAt), and removed playlists.
	Playlists []musicsource.RemotePlaylist
	// Tracks maps an ExternalID to that playlist's ordered tracks.
	Tracks map[string][]playlist.Track
	// ListErr / TracksErr / RefreshErr / VerifyErr, when set, are returned
	// instead of data.
	ListErr    error
	TracksErr  error
	RefreshErr error
	VerifyErr  error

	ListCalls   int
	TrackCalls  map[string]int
	Completed   string // the last value passed to Complete
	Refreshed   int
	VerifyCalls int
}

// New returns a fake for kind with empty data.
func New(kind musicsource.Kind) *Provider {
	return &Provider{ServiceKind: kind, Tracks: map[string][]playlist.Track{}, TrackCalls: map[string]int{}}
}

func (p *Provider) Kind() musicsource.Kind { return p.ServiceKind }

func (p *Provider) AuthRequest() (musicsource.AuthRequest, error) {
	return musicsource.AuthRequest{URL: "about:blank", RedirectPrefix: "about:blank?token="}, nil
}

func (p *Provider) Complete(_ context.Context, captured string) (musicsource.Session, error) {
	if captured == "" {
		return musicsource.Session{}, fmt.Errorf("fake: empty capture")
	}
	p.Completed = captured
	raw, _ := json.Marshal(map[string]string{"token": captured})
	return musicsource.Session{
		Kind:        p.ServiceKind,
		Raw:         raw,
		DisplayName: "Fake User",
	}, nil
}

func (p *Provider) Refresh(_ context.Context, s musicsource.Session) (musicsource.Session, error) {
	p.Refreshed++
	if p.RefreshErr != nil {
		return musicsource.Session{}, p.RefreshErr
	}
	return s, nil
}

func (p *Provider) VerifySession(_ context.Context, _ musicsource.Session) error {
	p.VerifyCalls++
	return p.VerifyErr
}

func (p *Provider) ListPlaylists(_ context.Context, _ musicsource.Session) ([]musicsource.RemotePlaylist, error) {
	p.ListCalls++
	if p.ListErr != nil {
		return nil, p.ListErr
	}
	out := make([]musicsource.RemotePlaylist, len(p.Playlists))
	copy(out, p.Playlists)
	return out, nil
}

func (p *Provider) PlaylistTracks(_ context.Context, _ musicsource.Session, externalID string) ([]playlist.Track, error) {
	p.TrackCalls[externalID]++
	if p.TracksErr != nil {
		return nil, p.TracksErr
	}
	tracks, ok := p.Tracks[externalID]
	if !ok {
		return nil, fmt.Errorf("fake: no tracks for %q", externalID)
	}
	out := make([]playlist.Track, len(tracks))
	copy(out, tracks)
	return out, nil
}
