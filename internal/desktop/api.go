// Package desktop exposes the narrow Go API bound into the Wails frontend.
package desktop

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"playlistforge/internal/app"
	"playlistforge/internal/credentials"
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

// API delegates desktop calls to the transport-independent application service.
type API struct {
	ctx       context.Context
	service   *app.Service
	keys      credentialStore
	validator keyValidator
	openURL   func(string)
}

// New creates the object registered in Wails' Bind list.
func New(ctx context.Context, service *app.Service, keys credentialStore, validator keyValidator, openURL func(string)) *API {
	return &API{ctx: ctx, service: service, keys: keys, validator: validator, openURL: openURL}
}

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

func (a *API) SaveKey(key string, allowPlaintext bool) (credentials.Status, error) {
	if err := a.validator.Validate(a.ctx, key); err != nil {
		return credentials.Status{}, err
	}
	return a.keys.Set(key, allowPlaintext)
}

func (a *API) DeleteKey() error { return a.keys.Delete() }

func (a *API) ListPlaylists() ([]playlist.Playlist, error) { return a.service.List(a.ctx) }

func (a *API) GetPlaylist(id string) (playlist.Playlist, error) { return a.service.Get(a.ctx, id) }

func (a *API) Generate(request playlist.GenerateRequest) (playlist.Job, error) {
	return a.service.Generate(request)
}

func (a *API) Refine(id, prompt string, effort playlist.Effort) (playlist.Job, error) {
	return a.service.Refine(id, prompt, effort)
}

func (a *API) RemoveTrack(playlistID, trackID string) (playlist.Playlist, error) {
	return a.service.DeleteTrack(a.ctx, playlistID, trackID)
}

func (a *API) ReplaceTrack(playlistID, trackID, prompt string, effort playlist.Effort) (playlist.Job, error) {
	return a.service.Replace(playlistID, trackID, prompt, effort)
}

func (a *API) CreateSoundiizHandoff(id string) (playlist.Job, error) {
	return a.service.Handoff(id)
}

func (a *API) GetJob(id string) (playlist.Job, error) {
	job, ok := a.service.GetJob(id)
	if !ok {
		return playlist.Job{}, errors.New("job not found")
	}
	return job, nil
}

func (a *API) CancelJob(id string) error { return a.service.CancelJob(id) }

// OpenExternalURL only permits the validated Soundiiz handoff origin produced
// by this application. It prevents a compromised frontend from becoming an
// unrestricted native URL launcher.
func (a *API) OpenExternalURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "soundiiz.com" || parsed.User != nil || !strings.HasPrefix(parsed.Path, "/go/import-playlist/") {
		return errors.New("refusing to open an untrusted external URL")
	}
	if a.openURL == nil {
		return errors.New("external URL handler unavailable")
	}
	a.openURL(raw)
	return nil
}
