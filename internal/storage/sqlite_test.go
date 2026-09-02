package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"playlistforge/internal/playlist"
)

func sampleRevision(id, playlistID string) playlist.Revision {
	year, remaster := 1971, 2023
	version, quality := "2023 Remaster", "24-bit"
	return playlist.Revision{ID: id, PlaylistID: playlistID, Title: "Evening", Description: "Warm", Prompt: "warm songs", TrackTarget: 2, Model: playlist.ModelGPTSol, Effort: playlist.EffortMedium, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC), Usage: playlist.Usage{TotalTokens: 42}, Tracks: []playlist.Track{{ID: "t1", Title: "One", Artists: []string{"Artist", "Guest"}, Album: "Album", ReleaseYear: &year, Version: &version, RemasterYear: &remaster, QualityNote: &quality, Rationale: "Fits"}, {ID: "t2", Title: "Two", Artists: []string{"Other"}, Album: "", Rationale: "Flows"}}}
}

func openTestRepo(t *testing.T) *Repository {
	t.Helper()
	repo, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := repo.Close(); err != nil {
			t.Error(err)
		}
	})
	return repo
}

func TestRepositoryLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := openTestRepo(t)
	created, err := repo.Create(ctx, sampleRevision("r1", "p1"), []string{"reference"})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "p1" || created.RevisionCount != 1 || len(created.CurrentRevision.Tracks) != 2 || created.CurrentRevision.Tracks[0].ReleaseYear == nil {
		t.Fatalf("bad create: %#v", created)
	}
	listed, err := repo.List(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list: %v %#v", err, listed)
	}
	next := sampleRevision("", "")
	next.Title = "Revised"
	next.CreatedAt = time.Time{}
	next.Tracks[0].ID = ""
	updated, err := repo.AddRevision(ctx, "p1", next)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RevisionCount != 2 || updated.CurrentRevision.Number != 2 || updated.CurrentRevision.Title != "Revised" || updated.CurrentRevision.Tracks[0].ID == "" {
		t.Fatalf("bad update: %#v", updated)
	}
	if _, err := repo.DeleteTrack(ctx, "p1", "missing"); err == nil {
		t.Fatal("expected missing track")
	}
	deleted, err := repo.DeleteTrack(ctx, "p1", updated.CurrentRevision.Tracks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.RevisionCount != 3 || len(deleted.CurrentRevision.Tracks) != 1 || deleted.CurrentRevision.Tracks[0].Position != 1 {
		t.Fatalf("bad delete: %#v", deleted)
	}
	if _, err := repo.DeleteTrack(ctx, "p1", deleted.CurrentRevision.Tracks[0].ID); err == nil {
		t.Fatal("removed the final track")
	}
	expires := time.Now().Add(time.Hour).Unix()
	if err := repo.SetSoundiiz(ctx, "p1", "https://soundiiz.com/go/import-playlist/a", expires); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SoundiizURL == nil || got.SoundiizExpires == nil {
		t.Fatalf("handoff not saved: %#v", got)
	}
}

func TestRepositoryErrorsAndDefaults(t *testing.T) {
	ctx := context.Background()
	repo := openTestRepo(t)
	if _, err := repo.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if _, err := repo.AddRevision(ctx, "missing", sampleRevision("r", "")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if _, err := repo.DeleteTrack(ctx, "missing", "t"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := repo.SetSoundiiz(ctx, "missing", "https://soundiiz.com/go/import-playlist/a", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	revision := sampleRevision("", "")
	revision.CreatedAt = time.Time{}
	created, err := repo.Create(ctx, revision, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.CurrentRevision.ID == "" || created.CreatedAt.IsZero() {
		t.Fatalf("defaults missing: %#v", created)
	}
	if _, err := repo.Create(ctx, sampleRevision("duplicate-r", created.ID), nil); err == nil {
		t.Fatal("expected duplicate playlist error")
	}
	if _, err := parseTime("bad"); err == nil {
		t.Fatal("expected time parse error")
	}
}
