package app

// Tests for the streaming-import pipeline against a real temp SQLite database
// and the fake provider: new playlists import and hydrate, an unchanged sync is
// a no-op, a changed playlist re-hydrates, a playlist gone upstream is
// hard-deleted, a playlist that holds the same music as an existing one merges
// into it, an import that failed to hydrate during sync hydrates lazily on
// open, and UnlinkSource splits a merged record and suppresses re-merge.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"playlistforge/internal/musicsource"
	msfake "playlistforge/internal/musicsource/fake"
	"playlistforge/internal/playlist"
	"playlistforge/internal/storage"
)

func isrcTracks(prefix string, n int) []playlist.Track {
	out := make([]playlist.Track, n)
	for i := 0; i < n; i++ {
		code := prefixCode(prefix, i)
		out[i] = playlist.Track{
			Title: prefix + " track", Artists: []string{prefix + " artist"},
			ISRC: &code, Rationale: "imported",
		}
	}
	return out
}

func prefixCode(prefix string, i int) string {
	reg := (prefix + "00")[:3]
	return "US" + reg + "24" + pad5(i)
}

func pad5(i int) string {
	s := "0000" + itoa5(i)
	return s[len(s)-5:]
}
func itoa5(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func newImportService(t *testing.T) (*Service, *storage.Repository, *msfake.Provider, *fakeSessions) {
	t.Helper()
	repo, err := storage.Open(filepath.Join(t.TempDir(), "import.db"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := newFakeSessions()
	tidal := msfake.New(musicsource.KindTIDAL)
	ctx, cancel := context.WithCancel(context.Background())
	svc := New(ctx, repo, &fakeGenerator{}, fakeImporter{}, sessions, musicsource.Registry{musicsource.KindTIDAL: tidal}, zap.NewNop())
	t.Cleanup(func() { svc.Close(); cancel(); _ = repo.Close() })
	// Pre-connect.
	if _, err := svc.CompleteAuth(ctx, musicsource.KindTIDAL, "token"); err != nil {
		t.Fatal(err)
	}
	return svc, repo, tidal, sessions
}

func remote(id, title, etag string, count int) musicsource.RemotePlaylist {
	return musicsource.RemotePlaylist{ExternalID: id, Title: title, ETag: etag, TrackCount: count, URL: "https://tidal.com/playlist/" + id}
}

func TestSyncImportsAndHydrates(t *testing.T) {
	svc, _, tidal, _ := newImportService(t)
	ctx := context.Background()

	tidal.Playlists = []musicsource.RemotePlaylist{remote("p1", "Rainy", "e1", 3), remote("p2", "Sunny", "e1", 4)}
	tidal.Tracks = map[string][]playlist.Track{"p1": isrcTracks("R", 3), "p2": isrcTracks("S", 4)}

	res, err := svc.SyncSource(ctx, musicsource.KindTIDAL)
	if err != nil || res.Added != 2 || res.Updated != 0 {
		t.Fatalf("first sync: %+v %v", res, err)
	}
	items, err := svc.List(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("list: %d %v", len(items), err)
	}
	var p1 playlist.Playlist
	for _, it := range items {
		if it.CurrentRevision.Title == "Rainy" {
			p1 = it
		}
	}
	if p1.Origin != playlist.OriginImported || len(p1.Sources) != 1 || p1.Sources[0].Kind != "tidal" {
		t.Fatalf("imported shape: %+v", p1)
	}
	got, err := svc.Get(ctx, p1.ID)
	if err != nil || len(got.CurrentRevision.Tracks) != 3 || got.TracksStale {
		t.Fatalf("hydrated: %d stale=%t %v", len(got.CurrentRevision.Tracks), got.TracksStale, err)
	}

	// Unchanged re-sync is a no-op.
	res, err = svc.SyncSource(ctx, musicsource.KindTIDAL)
	if err != nil || res.Added != 0 || res.Updated != 0 || res.Deleted != 0 {
		t.Fatalf("second sync: %+v %v", res, err)
	}

	// Change p1 (new ETag + different tracks) and drop p2.
	tidal.Playlists = []musicsource.RemotePlaylist{remote("p1", "Rainy (v2)", "e2", 5)}
	tidal.Tracks["p1"] = isrcTracks("R2", 5)
	res, err = svc.SyncSource(ctx, musicsource.KindTIDAL)
	if err != nil || res.Updated != 1 || res.Deleted != 1 {
		t.Fatalf("third sync: %+v %v", res, err)
	}
	got, _ = svc.Get(ctx, p1.ID)
	if got.CurrentRevision.Title != "Rainy (v2)" || len(got.CurrentRevision.Tracks) != 5 {
		t.Fatalf("after change: %q %d", got.CurrentRevision.Title, len(got.CurrentRevision.Tracks))
	}
	if items, _ := svc.List(ctx); len(items) != 1 {
		t.Fatalf("after delete: %d", len(items))
	}
}

func TestSyncMergesSameMusic(t *testing.T) {
	svc, _, tidal, _ := newImportService(t)
	ctx := context.Background()

	// A generated playlist with a known ISRC set.
	tracks := isrcTracks("G", 12)
	gen := playlist.GeneratedPlaylist{Title: "My Mix", Description: "d", Tracks: tracks}
	saved, err := svc.repo.Create(ctx, playlist.Revision{ID: "", PlaylistID: "", Title: gen.Title, Description: gen.Description, Tracks: tracks, CreatedAt: time.Now()}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The same music appears on TIDAL.
	tidal.Playlists = []musicsource.RemotePlaylist{remote("t1", "My Mix (TIDAL)", "e1", 12)}
	tidal.Tracks = map[string][]playlist.Track{"t1": isrcTracks("G", 12)}

	res, err := svc.SyncSource(ctx, musicsource.KindTIDAL)
	if err != nil || res.Merged != 1 || res.Added != 0 {
		t.Fatalf("merge sync: %+v %v", res, err)
	}
	if items, _ := svc.List(ctx); len(items) != 1 {
		t.Fatalf("merge produced %d rows, want 1", len(items))
	}
	merged, _ := svc.Get(ctx, saved.ID)
	if len(merged.Sources) != 1 || merged.Sources[0].Kind != "tidal" {
		t.Fatalf("target not linked: %+v", merged.Sources)
	}
	if merged.CurrentRevision.Title != "My Mix" {
		t.Fatalf("generated tracklist should win: %q", merged.CurrentRevision.Title)
	}

	// UnlinkSource splits it back out and blocks re-merge.
	after, err := svc.UnlinkSource(ctx, saved.ID, musicsource.KindTIDAL, "t1")
	if err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if len(after.Sources) != 0 {
		t.Fatalf("still linked after unlink: %+v", after.Sources)
	}
	if items, _ := svc.List(ctx); len(items) != 2 {
		t.Fatalf("after unlink want 2 rows, got %d", len(items))
	}
}

func TestSyncLazyHydrateAfterFailure(t *testing.T) {
	svc, _, tidal, _ := newImportService(t)
	ctx := context.Background()

	tidal.Playlists = []musicsource.RemotePlaylist{remote("p1", "Deferred", "e1", 2)}
	tidal.Tracks = map[string][]playlist.Track{"p1": isrcTracks("D", 2)}
	tidal.TracksErr = errors.New("network blip")

	res, err := svc.SyncSource(ctx, musicsource.KindTIDAL)
	if err != nil || res.Added != 1 {
		t.Fatalf("sync with hydrate failure: %+v %v", res, err)
	}
	items, _ := svc.List(ctx)
	if len(items) != 1 || !items[0].TracksStale {
		t.Fatalf("expected one stale import, got %+v", items)
	}

	tidal.TracksErr = nil
	got, err := svc.Get(ctx, items[0].ID)
	if err != nil || got.TracksStale || len(got.CurrentRevision.Tracks) != 2 {
		t.Fatalf("lazy hydrate: stale=%t tracks=%d %v", got.TracksStale, len(got.CurrentRevision.Tracks), err)
	}
}

func TestSyncSourceErrors(t *testing.T) {
	svc, _, tidal, _ := newImportService(t)
	ctx := context.Background()

	tidal.ListErr = errors.New("tidal down")
	if _, err := svc.SyncSource(ctx, musicsource.KindTIDAL); err == nil {
		t.Fatal("list failure should surface")
	}
	if _, err := svc.SyncSource(ctx, musicsource.KindQobuz); err == nil {
		t.Fatal("sync of an unconnected service should fail")
	}
}

func TestSyncChangeSignals(t *testing.T) {
	svc, _, tidal, _ := newImportService(t)
	ctx := context.Background()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	withTime := musicsource.RemotePlaylist{ExternalID: "u1", Title: "By time", UpdatedAt: t0, TrackCount: 2}
	noSignal := musicsource.RemotePlaylist{ExternalID: "n1", Title: "No signal", TrackCount: 2}
	tidal.Playlists = []musicsource.RemotePlaylist{withTime, noSignal}
	tidal.Tracks = map[string][]playlist.Track{"u1": isrcTracks("U", 2), "n1": isrcTracks("N", 2)}

	if res, err := svc.SyncSource(ctx, musicsource.KindTIDAL); err != nil || res.Added != 2 {
		t.Fatalf("initial: %+v %v", res, err)
	}
	// Same timestamp and no signal: both unchanged.
	if res, _ := svc.SyncSource(ctx, musicsource.KindTIDAL); res.Updated != 0 {
		t.Fatalf("expected no updates, got %+v", res)
	}
	// Advance the timestamp on u1 only.
	tidal.Playlists[0].UpdatedAt = t0.Add(time.Hour)
	tidal.Tracks["u1"] = isrcTracks("U2", 3)
	if res, _ := svc.SyncSource(ctx, musicsource.KindTIDAL); res.Updated != 1 {
		t.Fatalf("expected one update from the newer timestamp, got %+v", res)
	}
}

func TestGetHydrationEdges(t *testing.T) {
	svc, _, tidal, _ := newImportService(t)
	ctx := context.Background()

	// A generated playlist: Get returns it straight through.
	saved, err := svc.repo.Create(ctx, playlist.Revision{Title: "Gen", Description: "d", Tracks: isrcTracks("G", 3), CreatedAt: time.Now()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := svc.Get(ctx, saved.ID); err != nil || got.Origin != playlist.OriginGenerated {
		t.Fatalf("generated get: %+v %v", got.Origin, err)
	}
	if _, err := svc.Get(ctx, "no-such-id"); err == nil {
		t.Fatal("missing id should error")
	}

	// A stale import (hydration failed during sync).
	tidal.Playlists = []musicsource.RemotePlaylist{remote("p1", "Stale", "e1", 2)}
	tidal.Tracks = map[string][]playlist.Track{"p1": isrcTracks("D", 2)}
	tidal.TracksErr = errors.New("blip")
	if _, err := svc.SyncSource(ctx, musicsource.KindTIDAL); err != nil {
		t.Fatal(err)
	}
	items, _ := svc.List(ctx)
	var staleID string
	for _, it := range items {
		if it.TracksStale {
			staleID = it.ID
		}
	}
	if staleID == "" {
		t.Fatal("no stale import created")
	}

	// Get while the fetch still fails: returns the stale playlist, no error.
	if got, err := svc.Get(ctx, staleID); err != nil || !got.TracksStale {
		t.Fatalf("get during outage: stale=%t %v", got.TracksStale, err)
	}
	// Get while disconnected: same.
	tidal.TracksErr = nil
	if err := svc.Disconnect(musicsource.KindTIDAL); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.Get(ctx, staleID); err != nil || !got.TracksStale {
		t.Fatalf("get while disconnected: stale=%t %v", got.TracksStale, err)
	}
	// UnlinkSource on a disconnected service fails inside the follow-up sync.
	if _, err := svc.UnlinkSource(ctx, staleID, musicsource.KindTIDAL, "p1"); err == nil {
		t.Fatal("unlink should fail without a session")
	}
}
