package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"playlistforge/internal/playlist"
	"playlistforge/internal/soundiiz"
	"playlistforge/internal/storage"
)

type fakeGenerator struct {
	mu          sync.Mutex
	references  int
	generateErr error
	refineErr   error
	replaceErr  error
	replacement *playlist.Track
	block       bool
}

func generated(count int) playlist.GeneratedPlaylist {
	tracks := make([]playlist.Track, count)
	for i := range tracks {
		tracks[i] = playlist.Track{ID: fmt.Sprintf("track-%d-%d", count, i), Position: i + 1, Title: fmt.Sprintf("Song %d", i), Artists: []string{"Artist"}, Rationale: "Fits"}
	}
	return playlist.GeneratedPlaylist{Title: "Generated", Description: "Description", Tracks: tracks}
}
func (f *fakeGenerator) Generate(ctx context.Context, request playlist.GenerateRequest, refs []playlist.Revision) (playlist.GeneratedPlaylist, playlist.Usage, error) {
	f.mu.Lock()
	f.references = len(refs)
	block := f.block
	err := f.generateErr
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return playlist.GeneratedPlaylist{}, playlist.Usage{}, ctx.Err()
	}
	return generated(request.TrackCount), playlist.Usage{TotalTokens: 1}, err
}
func (f *fakeGenerator) Refine(_ context.Context, revision playlist.Revision, _ string, _ playlist.Effort) (playlist.GeneratedPlaylist, playlist.Usage, error) {
	return generated(len(revision.Tracks)), playlist.Usage{TotalTokens: 2}, f.refineErr
}
func (f *fakeGenerator) Replace(_ context.Context, _ playlist.Revision, _ string, _ string, _ playlist.Effort) (playlist.Track, playlist.Usage, error) {
	if f.replacement != nil {
		return *f.replacement, playlist.Usage{TotalTokens: 3}, f.replaceErr
	}
	return playlist.Track{ID: "replacement", Title: "Replacement", Artists: []string{"New"}, Rationale: "Flow"}, playlist.Usage{TotalTokens: 3}, f.replaceErr
}

type fakeImporter struct {
	result soundiiz.Result
	err    error
}

type classifiedError struct{}

func (classifiedError) Error() string         { return "provider detail" }
func (classifiedError) PublicCode() string    { return "credit_balance_exhausted" }
func (classifiedError) PublicMessage() string { return "Credits are exhausted." }

func (f fakeImporter) Import(context.Context, playlist.Revision) (soundiiz.Result, error) {
	return f.result, f.err
}

func testService(t *testing.T, generator *fakeGenerator, importer fakeImporter) (*Service, *storage.Repository) {
	t.Helper()
	repo, err := storage.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := New(ctx, repo, generator, importer, zap.NewNop())
	t.Cleanup(func() { service.Close(); cancel(); _ = repo.Close() })
	return service, repo
}

func await(t *testing.T, service *Service, id string) playlist.Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := service.GetJob(id)
		if !ok {
			t.Fatal("job disappeared")
		}
		if job.Status != playlist.JobQueued && job.Status != playlist.JobRunning {
			return job
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job timed out")
	return playlist.Job{}
}

func createReference(t *testing.T, repo *storage.Repository, id string) {
	t.Helper()
	_, err := repo.Create(context.Background(), playlist.Revision{ID: "rev-" + id, PlaylistID: id, Title: "Reference", Prompt: "ref", TrackTarget: 1, Model: playlist.ModelGPTSol, Effort: playlist.EffortMedium, Tracks: []playlist.Track{{ID: "reftrack-" + id, Title: "Old", Artists: []string{"A"}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGenerateListGetAndDelete(t *testing.T) {
	gen := &fakeGenerator{}
	service, repo := testService(t, gen, fakeImporter{})
	createReference(t, repo, "reference")
	if _, err := service.Generate(playlist.GenerateRequest{Prompt: "x", TrackCount: 20, Effort: playlist.EffortMedium}); !errors.Is(err, playlist.ErrInvalidPrompt) {
		t.Fatalf("validation = %v", err)
	}
	job, err := service.Generate(playlist.GenerateRequest{Prompt: "warm evening", TrackCount: 20, Effort: playlist.EffortMedium, ReferenceIDs: []string{"reference"}})
	if err != nil {
		t.Fatal(err)
	}
	done := await(t, service, job.ID)
	if done.Status != playlist.JobSucceeded || done.PlaylistID == "" {
		t.Fatalf("job = %#v", done)
	}
	if gen.references != 1 {
		t.Fatalf("references = %d", gen.references)
	}
	item, err := service.Get(context.Background(), done.PlaylistID)
	if err != nil || len(item.CurrentRevision.Tracks) != 20 {
		t.Fatalf("item=%#v err=%v", item, err)
	}
	gen.replacement = &playlist.Track{ID: "duplicate", Title: "Song 1", Artists: []string{"Artist"}, Rationale: "Duplicate"}
	duplicate, err := service.Replace(done.PlaylistID, item.CurrentRevision.Tracks[0].ID, "", playlist.EffortMedium)
	if err != nil {
		t.Fatal(err)
	}
	if failed := await(t, service, duplicate.ID); failed.Status != playlist.JobFailed || !strings.Contains(failed.Error, "duplicate track") {
		t.Fatalf("duplicate replacement = %#v", failed)
	}
	gen.replacement = nil
	items, err := service.List(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	updated, err := service.DeleteTrack(context.Background(), done.PlaylistID, item.CurrentRevision.Tracks[0].ID)
	if err != nil || len(updated.CurrentRevision.Tracks) != 19 {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if _, err := service.DeleteTrack(context.Background(), done.PlaylistID, ""); err == nil {
		t.Fatal("accepted blank track")
	}
	if err := service.CancelJob(done.ID); err == nil {
		t.Fatal("cancelled finished job")
	}
}

func TestGenerateReferenceAndGeneratorFailures(t *testing.T) {
	gen := &fakeGenerator{}
	service, _ := testService(t, gen, fakeImporter{})
	job, _ := service.Generate(playlist.GenerateRequest{Prompt: "valid prompt", TrackCount: 20, Effort: playlist.EffortMedium, ReferenceIDs: []string{"missing"}})
	if done := await(t, service, job.ID); done.Status != playlist.JobFailed || !strings.Contains(done.Error, "reference") {
		t.Fatalf("job = %#v", done)
	}
	gen.generateErr = errors.New("curation failed")
	job, _ = service.Generate(playlist.GenerateRequest{Prompt: "valid prompt", TrackCount: 20, Effort: playlist.EffortMedium})
	if done := await(t, service, job.ID); done.Status != playlist.JobFailed || done.Error != "curation failed" {
		t.Fatalf("job = %#v", done)
	}
	if _, ok := service.GetJob("missing"); ok {
		t.Fatal("found missing job")
	}
	if err := service.CancelJob("missing"); err == nil {
		t.Fatal("cancelled missing job")
	}
}

func TestRefineAndReplace(t *testing.T) {
	gen := &fakeGenerator{}
	service, repo := testService(t, gen, fakeImporter{})
	createReference(t, repo, "p")
	if _, err := service.Refine("p", "x", playlist.EffortMedium); !errors.Is(err, playlist.ErrInvalidPrompt) {
		t.Fatalf("prompt = %v", err)
	}
	if _, err := service.Refine("p", "valid", "bad"); !errors.Is(err, playlist.ErrInvalidEffort) {
		t.Fatalf("effort = %v", err)
	}
	job, err := service.Refine("p", "more energy", playlist.EffortHigh)
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, service, job.ID); done.Status != playlist.JobSucceeded {
		t.Fatalf("job = %#v", done)
	}
	item, _ := service.Get(context.Background(), "p")
	if item.RevisionCount != 2 || item.CurrentRevision.Usage.TotalTokens != 2 {
		t.Fatalf("item = %#v", item)
	}
	trackID := item.CurrentRevision.Tracks[0].ID
	if _, err := service.Replace("p", "", "", playlist.EffortMedium); err == nil {
		t.Fatal("accepted blank track")
	}
	if _, err := service.Replace("p", trackID, "", "bad"); !errors.Is(err, playlist.ErrInvalidEffort) {
		t.Fatalf("effort=%v", err)
	}
	if _, err := service.Replace("p", trackID, strings.Repeat("x", playlist.MaxPromptLen+1), playlist.EffortMedium); !errors.Is(err, playlist.ErrInvalidPrompt) {
		t.Fatalf("prompt=%v", err)
	}
	job, _ = service.Replace("p", trackID, "more modern", playlist.EffortMedium)
	if done := await(t, service, job.ID); done.Status != playlist.JobSucceeded {
		t.Fatalf("job = %#v", done)
	}
	item, _ = service.Get(context.Background(), "p")
	if item.CurrentRevision.Tracks[0].Title != "Replacement" || item.RevisionCount != 3 {
		t.Fatalf("item = %#v", item)
	}
	job, _ = service.Replace("p", "missing", "x", playlist.EffortMedium)
	if done := await(t, service, job.ID); done.Status != playlist.JobFailed {
		t.Fatalf("job = %#v", done)
	}
	gen.refineErr = errors.New("refine failed")
	job, _ = service.Refine("p", "valid prompt", playlist.EffortMedium)
	if done := await(t, service, job.ID); done.Status != playlist.JobFailed {
		t.Fatalf("job=%#v", done)
	}
	gen.replaceErr = errors.New("replace failed")
	job, _ = service.Replace("p", item.CurrentRevision.Tracks[0].ID, "", playlist.EffortMedium)
	if done := await(t, service, job.ID); done.Status != playlist.JobFailed {
		t.Fatalf("job=%#v", done)
	}
}

func TestHandoff(t *testing.T) {
	gen := &fakeGenerator{}
	importer := fakeImporter{result: soundiiz.Result{ShareURL: "https://soundiiz.com/go/import-playlist/token", ExpiresAt: time.Now().Add(time.Hour).Unix(), Tracks: 1}}
	service, repo := testService(t, gen, importer)
	createReference(t, repo, "p")
	job, err := service.Handoff("p")
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, service, job.ID); done.Status != playlist.JobSucceeded {
		t.Fatalf("job=%#v", done)
	}
	item, _ := service.Get(context.Background(), "p")
	if item.SoundiizURL == nil {
		t.Fatalf("item=%#v", item)
	}
	service.importer = fakeImporter{result: soundiiz.Result{Tracks: 0}}
	job, _ = service.Handoff("p")
	if done := await(t, service, job.ID); done.Status != playlist.JobFailed || !strings.Contains(done.Error, "accepted") {
		t.Fatalf("job=%#v", done)
	}
	service.importer = fakeImporter{err: errors.New("soundiiz down")}
	job, _ = service.Handoff("p")
	if done := await(t, service, job.ID); done.Status != playlist.JobFailed {
		t.Fatalf("job=%#v", done)
	}
}

func TestCancelAndPublicError(t *testing.T) {
	gen := &fakeGenerator{block: true}
	service, _ := testService(t, gen, fakeImporter{})
	job, _ := service.Generate(playlist.GenerateRequest{Prompt: "long prompt", TrackCount: 20, Effort: playlist.EffortMedium})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, _ := service.GetJob(job.ID)
		if current.Status == playlist.JobRunning {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := service.CancelJob(job.ID); err != nil {
		t.Fatal(err)
	}
	if done := await(t, service, job.ID); done.Status != playlist.JobCancelled {
		t.Fatalf("job=%#v", done)
	}
	secret := publicError(errors.New("bad " + strings.Join([]string{"s", "k-secret"}, "")))
	if strings.Contains(secret, "secret") {
		t.Fatalf("not redacted: %s", secret)
	}
	long := publicError(errors.New(strings.Repeat("x", 700)))
	if len(long) != 500 {
		t.Fatalf("length=%d", len(long))
	}
	message, code := publicFailure(fmt.Errorf("generate: %w", classifiedError{}))
	if message != "Credits are exhausted." || code != "credit_balance_exhausted" {
		t.Fatalf("message=%q code=%q", message, code)
	}
}
