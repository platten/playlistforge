// Package desktop exposes the narrow Go API bound into the Wails frontend.
package desktop

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"playlistforge/internal/app"
	"playlistforge/internal/credentials"
	"playlistforge/internal/musicsource"
	"playlistforge/internal/playlist"
)

type credentialStore interface {
	Status() credentials.Status
	Set(string, bool) (credentials.Status, error)
	Delete() error
}

type keyValidator interface {
	Validate(context.Context, string) error
}

// Config is the stable presentation contract shared with the React UI.
type Config struct {
	Credential  credentials.Status `json:"credential"`
	Model       string             `json:"model"`
	TrackCounts []int              `json:"trackCounts"`
	Efforts     []playlist.Effort  `json:"efforts"`
	Pricing     Pricing            `json:"pricing"`
}

// Pricing describes the embedded estimate rate card without exposing pointers.
type Pricing struct {
	Version               string  `json:"version"`
	InputPerMillion       float64 `json:"inputPerMillion"`
	CachedInputPerMillion float64 `json:"cachedInputPerMillion"`
	OutputPerMillion      float64 `json:"outputPerMillion"`
	WebSearchFeeKnown     bool    `json:"webSearchFeeKnown"`
}

// API is the single Go service registered with Wails. Every exported method is
// callable from the React frontend by its fully-qualified name; each one is a
// thin adapter over the transport-independent application service so the domain
// never depends on the desktop runtime.
type API struct {
	// ctx is the process context captured at startup. Wails v3 has no
	// per-request context on the frontend transport, so read operations reuse
	// this one and are cancelled only when the application shuts down.
	ctx       context.Context
	service   *app.Service
	keys      credentialStore
	validator keyValidator
	// openURL hands a vetted URL to the host browser. It is injected (rather
	// than called directly) so the package stays free of a Wails import and
	// OpenExternalURL remains unit-testable.
	openURL func(string)
	// runAuth opens the streaming sign-in window described by req and returns
	// the value it captured (a redirect URL or an extracted token). Injected
	// for the same reasons as openURL.
	runAuth func(req musicsource.AuthRequest) (string, error)
}

// New builds the desktop API. The caller registers the result as a Wails
// service and supplies openURL and runAuth, wired to the host webview.
func New(ctx context.Context, service *app.Service, keys credentialStore, validator keyValidator, openURL func(string), runAuth func(musicsource.AuthRequest) (string, error)) *API {
	return &API{ctx: ctx, service: service, keys: keys, validator: validator, openURL: openURL, runAuth: runAuth}
}

// Config returns the immutable presentation contract: credential status, the
// fixed model id, the selectable track counts and reasoning efforts, and the
// current rate card used for cost estimates.
func (a *API) Config() Config {
	return Config{
		Credential:  a.keys.Status(),
		Model:       playlist.ModelGPTSol,
		TrackCounts: []int{20, 30, 40, 50, 60, 100},
		Efforts:     []playlist.Effort{playlist.EffortMedium, playlist.EffortHigh, playlist.EffortXHigh, playlist.EffortMax},
		Pricing: Pricing{
			Version:               playlist.CurrentPricing.Version,
			InputPerMillion:       playlist.CurrentPricing.InputPerMillion,
			CachedInputPerMillion: playlist.CurrentPricing.CachedInputPerMillion,
			OutputPerMillion:      playlist.CurrentPricing.OutputPerMillion,
			WebSearchFeeKnown:     playlist.CurrentPricing.WebSearchPerCall != nil,
		},
	}
}

// SaveKey verifies the key against OpenAI (key validity and model access) and,
// only if that succeeds, stores it. allowPlaintext permits the restricted
// config-file fallback when the OS credential store is unavailable.
func (a *API) SaveKey(key string, allowPlaintext bool) (credentials.Status, error) {
	if err := a.validator.Validate(a.ctx, key); err != nil {
		return credentials.Status{}, err
	}
	return a.keys.Set(key, allowPlaintext)
}

// DeleteKey removes any stored key from both the keyring and the fallback file.
func (a *API) DeleteKey() error { return a.keys.Delete() }

// ListPlaylists returns saved playlists, most recently updated first.
func (a *API) ListPlaylists() ([]playlist.Playlist, error) { return a.service.List(a.ctx) }

// GetPlaylist returns one playlist with its active revision.
func (a *API) GetPlaylist(id string) (playlist.Playlist, error) { return a.service.Get(a.ctx, id) }

// Generate validates the request and queues a new playlist job, returning the
// queued job immediately. The frontend polls GetJob for progress.
func (a *API) Generate(request playlist.GenerateRequest) (playlist.Job, error) {
	return a.service.Generate(request)
}

// Refine queues a job that rewrites the whole current revision to the prompt
// while keeping the track count. Returns the queued job.
func (a *API) Refine(id, prompt string, effort playlist.Effort) (playlist.Job, error) {
	return a.service.Refine(id, prompt, effort)
}

// RemoveTrack drops one track by writing a new revision, preserving history.
// It returns the updated playlist directly (no job).
func (a *API) RemoveTrack(playlistID, trackID string) (playlist.Playlist, error) {
	return a.service.DeleteTrack(a.ctx, playlistID, trackID)
}

// ReplaceTrack queues a job that swaps a single track for a fresh candidate,
// keeping its position. Returns the queued job.
func (a *API) ReplaceTrack(playlistID, trackID, prompt string, effort playlist.Effort) (playlist.Job, error) {
	return a.service.Replace(playlistID, trackID, prompt, effort)
}

// CreateSoundiizHandoff queues a job that asks Soundiiz for a temporary import
// link for the current revision. Returns the queued job; the link lands on the
// playlist once the job succeeds.
func (a *API) CreateSoundiizHandoff(id string) (playlist.Job, error) {
	return a.service.Handoff(id)
}

// GetJob returns the current snapshot of an in-memory job, or an error once the
// job id is no longer known to this process.
func (a *API) GetJob(id string) (playlist.Job, error) {
	job, ok := a.service.GetJob(id)
	if !ok {
		return playlist.Job{}, errors.New("job not found")
	}
	return job, nil
}

// CancelJob requests cancellation of a queued or running job.
func (a *API) CancelJob(id string) error { return a.service.CancelJob(id) }

// OpenExternalURL permits the validated Soundiiz handoff origin and the exact
// OpenAI billing recovery page. It prevents a compromised frontend from
// becoming an unrestricted native URL launcher.
func (a *API) OpenExternalURL(raw string) error {
	parsed, err := url.Parse(raw)
	trustedSoundiiz := parsed != nil && parsed.Host == "soundiiz.com" && strings.HasPrefix(parsed.Path, "/go/import-playlist/")
	trustedOpenAI := parsed != nil && parsed.Host == "platform.openai.com" && parsed.Path == "/settings/organization/billing/overview" && parsed.RawQuery == "" && parsed.Fragment == ""
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || (!trustedSoundiiz && !trustedOpenAI) {
		return errors.New("refusing to open an untrusted external URL")
	}
	if a.openURL == nil {
		return errors.New("external URL handler unavailable")
	}
	a.openURL(raw)
	return nil
}

// Connections reports the status of every streaming service the build offers.
func (a *API) Connections() []app.ConnectionStatus { return a.service.Connections() }

// ConnectService runs the sign-in window for kind and stores the resulting
// session. The returned status reflects the connection after sign-in.
func (a *API) ConnectService(kind string) (app.ConnectionStatus, error) {
	if a.runAuth == nil {
		return app.ConnectionStatus{}, errors.New("streaming sign-in is unavailable")
	}
	req, err := a.service.AuthRequest(musicsource.Kind(kind))
	if err != nil {
		return app.ConnectionStatus{}, err
	}
	captured, err := a.runAuth(req)
	if err != nil {
		return app.ConnectionStatus{}, err
	}
	return a.service.CompleteAuth(a.ctx, musicsource.Kind(kind), captured)
}

// DisconnectService removes the stored session for kind. Imported playlists are
// kept and can still be used as inspiration.
func (a *API) DisconnectService(kind string) error {
	return a.service.Disconnect(musicsource.Kind(kind))
}

// SyncSource refreshes the local mirror of one streaming service and returns a
// summary of what changed. Synchronous; no OpenAI cost.
func (a *API) SyncSource(kind string) (app.SyncResult, error) {
	return a.service.SyncSource(a.ctx, musicsource.Kind(kind))
}

// UnlinkSource detaches one streaming service from a merged playlist and
// prevents that pair from being auto-merged again.
func (a *API) UnlinkSource(playlistID, kind, externalID string) (playlist.Playlist, error) {
	return a.service.UnlinkSource(a.ctx, playlistID, musicsource.Kind(kind), externalID)
}
