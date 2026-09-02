// Package musicsource is the port for reading a listener's existing playlists
// from a streaming service. Adapters (internal/musicsource/tidal,
// internal/musicsource/qobuz) implement Provider; the application service is the
// only caller. It imports internal/playlist for the shared Track type and
// nothing else provider-specific.
package musicsource

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"playlistforge/internal/playlist"
)

// Kind identifies a streaming service.
type Kind string

const (
	KindTIDAL Kind = "tidal"
	KindQobuz Kind = "qobuz"
)

// Valid reports whether k is a supported service.
func (k Kind) Valid() bool { return k == KindTIDAL || k == KindQobuz }

// ErrNotConnected is returned by callers when no session exists for a service.
var ErrNotConnected = errors.New("streaming service is not connected")

// RemotePlaylist is a playlist as it exists on the service, before its tracks
// are fetched. TrackCount comes from the list response; PlaylistTracks fetches
// the ordered contents on demand.
type RemotePlaylist struct {
	ExternalID  string
	Title       string
	Description string
	TrackCount  int
	UpdatedAt   time.Time // service-side last-modified time; zero if unknown
	URL         string    // public web URL of the playlist
	ETag        string    // opaque change token; empty if the service has none
}

// Session is a provider's stored authentication state. Raw is opaque to
// everyone but the provider that produced it; the application service persists
// it verbatim in the OS keyring.
type Session struct {
	Kind        Kind            `json:"kind"`
	Raw         json.RawMessage `json:"raw"`
	DisplayName string          `json:"displayName"` // "signed in as ..." for the UI
	ExpiresAt   time.Time       `json:"expiresAt"`   // zero == no known expiry
}

// AuthRequest describes the sign-in window the desktop layer must open.
type AuthRequest struct {
	// URL is the page to load in the auth window.
	URL string
	// RedirectPrefix, when set, means "capture the first navigation whose URL
	// starts with this and hand its full URL to Complete".
	RedirectPrefix string
	// ExtractJS, when set, is evaluated in the auth window after each
	// navigation; a non-empty result is handed to Complete. Used when the
	// service has no OAuth redirect (the value lives in page state).
	ExtractJS string
}

// Provider adapts one streaming service to playlist reads.
type Provider interface {
	Kind() Kind

	// AuthRequest returns what the sign-in window should do.
	AuthRequest() (AuthRequest, error)
	// Complete turns the value captured by the auth window (a redirect URL or an
	// extracted token) into a Session.
	Complete(ctx context.Context, captured string) (Session, error)
	// Refresh renews a Session that is near expiry. A provider with no refresh
	// mechanism returns the session unchanged.
	Refresh(ctx context.Context, s Session) (Session, error)

	// ListPlaylists returns every playlist the user owns, paginating internally.
	ListPlaylists(ctx context.Context, s Session) ([]RemotePlaylist, error)
	// PlaylistTracks returns the ordered tracks of one playlist. Track.ISRC is
	// populated from the service where available.
	PlaylistTracks(ctx context.Context, s Session, externalID string) ([]playlist.Track, error)
}

// Registry maps a Kind to its Provider. A Kind with no entry is simply not
// offered in the UI.
type Registry map[Kind]Provider

// Get returns the provider for k, or ErrNotConnected-style nil handling is left
// to the caller.
func (r Registry) Get(k Kind) (Provider, bool) {
	p, ok := r[k]
	return p, ok
}
