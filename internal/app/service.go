// Package app coordinates playlist use cases without depending on HTTP or UI
// details. Its small interfaces keep paid APIs and persistence replaceable in
// tests and future integrations.
package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"playlistforge/internal/playlist"
	"playlistforge/internal/soundiiz"
)

var openAIKeyPattern = regexp.MustCompile(`(?i)\bsk-[a-z0-9_-]{4,}\b`)

type importer interface {
	Import(context.Context, playlist.Revision) (soundiiz.Result, error)
}

// Service coordinates asynchronous playlist operations and local persistence.
// Jobs live only for the lifetime of the process; completed playlists and
// revisions are persisted through the Repository interface.
type Service struct {
	repo      playlist.Repository
	generator playlist.Generator
	importer  importer
	logger    *zap.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.RWMutex
	jobs      map[string]playlist.Job
	cancels   map[string]context.CancelFunc
	// gate serializes paid, long-running operations. A single local user gets
	// predictable API spend and SQLite writes never compete with another job.
	gate    chan struct{}
	workers sync.WaitGroup
	now     func() time.Time
}

// New constructs a Service whose jobs are cancelled when parent is cancelled.
func New(parent context.Context, repo playlist.Repository, generator playlist.Generator, importer importer, logger *zap.Logger) *Service {
	ctx, cancel := context.WithCancel(parent)
	return &Service{repo: repo, generator: generator, importer: importer, logger: logger, ctx: ctx, cancel: cancel, jobs: make(map[string]playlist.Job), cancels: make(map[string]context.CancelFunc), gate: make(chan struct{}, 1), now: time.Now}
}

// Close cancels queued and running jobs and waits for their goroutines to exit.
func (s *Service) Close() {
	s.cancel()
	s.workers.Wait()
}

// List returns saved playlists in repository-defined order.
func (s *Service) List(ctx context.Context) ([]playlist.Playlist, error) { return s.repo.List(ctx) }

// Get returns one playlist with its active revision.
func (s *Service) Get(ctx context.Context, id string) (playlist.Playlist, error) {
	return s.repo.Get(ctx, id)
}

// DeleteTrack creates a new revision without trackID.
func (s *Service) DeleteTrack(ctx context.Context, playlistID, trackID string) (playlist.Playlist, error) {
	if strings.TrimSpace(trackID) == "" {
		return playlist.Playlist{}, errors.New("track ID is required")
	}
	return s.repo.DeleteTrack(ctx, playlistID, trackID)
}

// GetJob returns a snapshot of an in-memory job.
func (s *Service) GetJob(id string) (playlist.Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	return job, ok
}

// CancelJob requests cancellation of a queued or running job.
func (s *Service) CancelJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return errors.New("job not found")
	}
	if job.Status == playlist.JobSucceeded || job.Status == playlist.JobFailed || job.Status == playlist.JobCancelled {
		return errors.New("job has already finished")
	}
	if cancel := s.cancels[id]; cancel != nil {
		cancel()
	}
	return nil
}

// Generate validates request synchronously and queues playlist generation.
func (s *Service) Generate(request playlist.GenerateRequest) (playlist.Job, error) {
	if err := playlist.ValidateGenerateRequest(request); err != nil {
		return playlist.Job{}, err
	}
	return s.submit("Waiting to curate", func(ctx context.Context, jobID string) (string, error) {
		s.phase(jobID, "Loading reference playlists")
		references := make([]playlist.Revision, 0, len(request.ReferenceIDs))
		for _, id := range request.ReferenceIDs {
			item, err := s.repo.Get(ctx, id)
			if err != nil {
				return "", fmt.Errorf("load reference playlist: %w", err)
			}
			references = append(references, item.CurrentRevision)
		}
		s.phase(jobID, "Researching and curating tracks")
		generated, usage, err := s.generator.Generate(ctx, request, references)
		if err != nil {
			return "", err
		}
		s.logger.Debug("generated tracklist", zap.Any("tracks", generated.Tracks))
		now := s.now().UTC()
		revision := playlist.Revision{ID: uuid.NewString(), PlaylistID: uuid.NewString(), Title: generated.Title, Description: generated.Description, Prompt: request.Prompt, TrackTarget: request.TrackCount, Model: playlist.ModelGPTSol, Effort: request.Effort, Tracks: generated.Tracks, Usage: usage, CreatedAt: now}
		s.phase(jobID, "Saving playlist")
		saved, err := s.repo.Create(ctx, revision, request.ReferenceIDs)
		if err != nil {
			return "", err
		}
		return saved.ID, nil
	})
}

// Refine queues a full-playlist revision based on the current revision.
func (s *Service) Refine(playlistID, prompt string, effort playlist.Effort) (playlist.Job, error) {
	prompt = strings.TrimSpace(prompt)
	if len([]rune(prompt)) < playlist.MinPromptLen || len([]rune(prompt)) > playlist.MaxPromptLen {
		return playlist.Job{}, playlist.ErrInvalidPrompt
	}
	if !effort.Valid() {
		return playlist.Job{}, playlist.ErrInvalidEffort
	}
	return s.submit("Waiting to refine", func(ctx context.Context, jobID string) (string, error) {
		current, err := s.repo.Get(ctx, playlistID)
		if err != nil {
			return "", err
		}
		s.phase(jobID, "Researching refinements")
		generated, usage, err := s.generator.Refine(ctx, current.CurrentRevision, prompt, effort)
		if err != nil {
			return "", err
		}
		revision := playlist.Revision{Title: generated.Title, Description: generated.Description, Prompt: prompt, TrackTarget: len(generated.Tracks), Model: playlist.ModelGPTSol, Effort: effort, Tracks: generated.Tracks, Usage: usage, CreatedAt: s.now().UTC()}
		s.phase(jobID, "Saving revision")
		if _, err := s.repo.AddRevision(ctx, playlistID, revision); err != nil {
			return "", err
		}
		return playlistID, nil
	})
}

// Replace queues a one-track replacement and rejects duplicates before saving.
func (s *Service) Replace(playlistID, trackID, prompt string, effort playlist.Effort) (playlist.Job, error) {
	if strings.TrimSpace(trackID) == "" {
		return playlist.Job{}, errors.New("track ID is required")
	}
	if !effort.Valid() {
		return playlist.Job{}, playlist.ErrInvalidEffort
	}
	if len([]rune(prompt)) > playlist.MaxPromptLen {
		return playlist.Job{}, playlist.ErrInvalidPrompt
	}
	return s.submit("Waiting to replace track", func(ctx context.Context, jobID string) (string, error) {
		current, err := s.repo.Get(ctx, playlistID)
		if err != nil {
			return "", err
		}
		s.phase(jobID, "Researching a replacement")
		replacement, usage, err := s.generator.Replace(ctx, current.CurrentRevision, trackID, prompt, effort)
		if err != nil {
			return "", err
		}
		tracks := slices.Clone(current.CurrentRevision.Tracks)
		found := false
		for i := range tracks {
			if tracks[i].ID == trackID {
				replacement.Position = i + 1
				tracks[i] = replacement
				found = true
				break
			}
		}
		if !found {
			return "", errors.New("track not found")
		}
		validated := playlist.GeneratedPlaylist{
			Title:       current.CurrentRevision.Title,
			Description: current.CurrentRevision.Description,
			Tracks:      tracks,
		}
		if err := playlist.ValidateGenerated(validated, len(tracks)); err != nil {
			return "", fmt.Errorf("validate replacement: %w", err)
		}
		revision := current.CurrentRevision
		revision.ID = ""
		revision.Prompt = "Replace track: " + strings.TrimSpace(prompt)
		revision.Effort = effort
		revision.Tracks = tracks
		revision.Usage = usage
		revision.CreatedAt = s.now().UTC()
		s.phase(jobID, "Saving replacement")
		if _, err := s.repo.AddRevision(ctx, playlistID, revision); err != nil {
			return "", err
		}
		return playlistID, nil
	})
}

// Handoff queues creation of one generic Soundiiz import link. Soundiiz
// performs catalog matching and destination selection on its site.
func (s *Service) Handoff(playlistID string) (playlist.Job, error) {
	return s.submit("Waiting for Soundiiz", func(ctx context.Context, jobID string) (string, error) {
		current, err := s.repo.Get(ctx, playlistID)
		if err != nil {
			return "", err
		}
		s.phase(jobID, "Creating a secure Soundiiz handoff")
		result, err := s.importer.Import(ctx, current.CurrentRevision)
		if err != nil {
			return "", err
		}
		if result.Tracks != len(current.CurrentRevision.Tracks) {
			return "", fmt.Errorf("handoff accepted %d of %d tracks from Soundiiz", result.Tracks, len(current.CurrentRevision.Tracks))
		}
		if err := s.repo.SetSoundiiz(ctx, playlistID, result.ShareURL, result.ExpiresAt); err != nil {
			return "", err
		}
		return playlistID, nil
	})
}

type work func(context.Context, string) (string, error)

func (s *Service) submit(phase string, fn work) (playlist.Job, error) {
	id := uuid.NewString()
	job := playlist.Job{ID: id, Status: playlist.JobQueued, Phase: phase}
	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	s.jobs[id] = job
	s.cancels[id] = cancel
	s.mu.Unlock()
	s.workers.Add(1)
	go s.run(ctx, id, fn)
	return job, nil
}

func (s *Service) run(ctx context.Context, id string, fn work) {
	defer s.workers.Done()
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		s.finish(id, "", ctx.Err())
		return
	}
	defer func() { <-s.gate }()
	now := s.now().UTC()
	s.mu.Lock()
	job := s.jobs[id]
	job.Status = playlist.JobRunning
	job.StartedAt = &now
	s.jobs[id] = job
	s.mu.Unlock()
	playlistID, err := fn(ctx, id)
	s.finish(id, playlistID, err)
}

func (s *Service) phase(id, phase string) {
	s.mu.Lock()
	job := s.jobs[id]
	job.Phase = phase
	s.jobs[id] = job
	s.mu.Unlock()
}

func (s *Service) finish(id, playlistID string, err error) {
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[id]
	job.FinishedAt = &now
	job.PlaylistID = playlistID
	if err == nil {
		job.Status = playlist.JobSucceeded
		job.Phase = "Complete"
	} else if errors.Is(err, context.Canceled) {
		job.Status = playlist.JobCancelled
		job.Phase = "Cancelled"
	} else {
		job.Status = playlist.JobFailed
		job.Phase = "Failed"
		// Keep detailed errors in redacted logs while returning a bounded,
		// secret-filtered message to the frontend.
		job.Error, job.ErrorCode = publicFailure(err)
		s.logger.Error("background operation failed", zap.String("job_id", id), zap.Error(err))
	}
	s.jobs[id] = job
	delete(s.cancels, id)
}

// maxPublicErrorLen bounds the message forwarded to the frontend so a verbose
// upstream error cannot flood the UI or a log line.
const maxPublicErrorLen = 500

// publicError is the fallback path for errors that do not implement
// publicFailureError: redact anything that looks like an API key, then cap the
// length on a rune boundary so the result stays valid UTF-8.
func publicError(err error) string {
	message := openAIKeyPattern.ReplaceAllString(err.Error(), "[redacted]")
	if len(message) <= maxPublicErrorLen {
		return message
	}
	truncated := message[:maxPublicErrorLen]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}

type publicFailureError interface {
	PublicCode() string
	PublicMessage() string
}

func publicFailure(err error) (string, string) {
	var classified publicFailureError
	if errors.As(err, &classified) {
		return classified.PublicMessage(), classified.PublicCode()
	}
	return publicError(err), ""
}
