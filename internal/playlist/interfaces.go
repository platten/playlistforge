package playlist

import (
	"context"
	"time"
)

// Generator is the model-provider port used by the application service.
type Generator interface {
	Generate(ctx context.Context, request GenerateRequest, references []Revision) (GeneratedPlaylist, Usage, error)
	Refine(ctx context.Context, revision Revision, prompt string, effort Effort) (GeneratedPlaylist, Usage, error)
	Replace(ctx context.Context, revision Revision, trackID, prompt string, effort Effort) (Track, Usage, error)
}

// Repository is the persistence port used by the application service.
type Repository interface {
	Create(ctx context.Context, revision Revision, references []string) (Playlist, error)
	List(ctx context.Context) ([]Playlist, error)
	Get(ctx context.Context, id string) (Playlist, error)
	AddRevision(ctx context.Context, playlistID string, revision Revision) (Playlist, error)
	DeleteTrack(ctx context.Context, playlistID, trackID string) (Playlist, error)
	SetSoundiiz(ctx context.Context, playlistID, url string, expiresAt int64) error

	// Streaming-import persistence. All identifiers are the service's own.
	//
	// ListSourceLinks returns every stored link for one service so sync can
	// diff local state against the service's current playlist list.
	ListSourceLinks(ctx context.Context, kind string) ([]SourceLink, error)
	// CreateImported inserts an origin=imported playlist with an empty revision
	// and one source link (tracks_fetched = 0), returning its id.
	CreateImported(ctx context.Context, in SourceInput, syncedAt time.Time) (string, error)
	// TouchSourceLink bumps only synced_at for an unchanged playlist.
	TouchSourceLink(ctx context.Context, kind, externalID string, syncedAt time.Time) error
	// MarkSourceChanged records a new etag/remote time and resets tracks_fetched
	// so the next hydration re-pulls the tracklist.
	MarkSourceChanged(ctx context.Context, in SourceInput, syncedAt time.Time) error
	// SetImportedTracks replaces the single revision's tracks for a playlist and
	// marks its link hydrated.
	SetImportedTracks(ctx context.Context, kind, externalID string, tracks []Track) error
	// MergeSourceLink deletes the provisional imported playlist and re-attaches
	// its source link to an existing playlist that holds the same music.
	MergeSourceLink(ctx context.Context, shellPlaylistID, targetPlaylistID string) error
	// RemoveSourceLink drops a link; if that leaves an imported playlist with no
	// links it is hard-deleted along with any inbound references.
	RemoveSourceLink(ctx context.Context, kind, externalID string) (deletedPlaylist bool, err error)
	// SuppressMatch / MatchSuppressed record and read the manual-unlink override.
	SuppressMatch(ctx context.Context, playlistID, kind, externalID string) error
	MatchSuppressed(ctx context.Context, playlistID, kind, externalID string) (bool, error)
}
