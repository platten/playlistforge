package playlist

import "context"

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
	SetDestinations(ctx context.Context, playlistID string, destinations []string) error
	SetSoundiiz(ctx context.Context, playlistID, url string, expiresAt int64) error
}
