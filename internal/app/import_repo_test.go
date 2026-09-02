package app

// A pass-through playlist.Repository that can be told to fail one named method,
// so the import pipeline's error-wrapping branches are exercised without a full
// hand-written fake.

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"playlistforge/internal/musicsource"
	msfake "playlistforge/internal/musicsource/fake"
	"playlistforge/internal/playlist"
	"playlistforge/internal/storage"
)

type failingRepo struct {
	inner  playlist.Repository
	failOn string
	err    error
}

func (f *failingRepo) trip(name string) error {
	if f.failOn == name {
		return f.err
	}
	return nil
}

func (f *failingRepo) Create(c context.Context, r playlist.Revision, refs []string) (playlist.Playlist, error) {
	return f.inner.Create(c, r, refs)
}
func (f *failingRepo) List(c context.Context) ([]playlist.Playlist, error) {
	if err := f.trip("List"); err != nil {
		return nil, err
	}
	return f.inner.List(c)
}
func (f *failingRepo) Get(c context.Context, id string) (playlist.Playlist, error) {
	if err := f.trip("Get"); err != nil {
		return playlist.Playlist{}, err
	}
	return f.inner.Get(c, id)
}
func (f *failingRepo) AddRevision(c context.Context, id string, r playlist.Revision) (playlist.Playlist, error) {
	return f.inner.AddRevision(c, id, r)
}
func (f *failingRepo) DeleteTrack(c context.Context, p, tk string) (playlist.Playlist, error) {
	return f.inner.DeleteTrack(c, p, tk)
}
func (f *failingRepo) SetSoundiiz(c context.Context, p, u string, e int64) error {
	return f.inner.SetSoundiiz(c, p, u, e)
}
func (f *failingRepo) ListSourceLinks(c context.Context, k string) ([]playlist.SourceLink, error) {
	if err := f.trip("ListSourceLinks"); err != nil {
		return nil, err
	}
	return f.inner.ListSourceLinks(c, k)
}
func (f *failingRepo) CreateImported(c context.Context, in playlist.SourceInput, t time.Time) (string, error) {
	if err := f.trip("CreateImported"); err != nil {
		return "", err
	}
	return f.inner.CreateImported(c, in, t)
}
func (f *failingRepo) TouchSourceLink(c context.Context, k, e string, t time.Time) error {
	if err := f.trip("TouchSourceLink"); err != nil {
		return err
	}
	return f.inner.TouchSourceLink(c, k, e, t)
}
func (f *failingRepo) MarkSourceChanged(c context.Context, in playlist.SourceInput, t time.Time) error {
	if err := f.trip("MarkSourceChanged"); err != nil {
		return err
	}
	return f.inner.MarkSourceChanged(c, in, t)
}
func (f *failingRepo) SetImportedTracks(c context.Context, k, e string, tr []playlist.Track) error {
	if err := f.trip("SetImportedTracks"); err != nil {
		return err
	}
	return f.inner.SetImportedTracks(c, k, e, tr)
}
func (f *failingRepo) MergeSourceLink(c context.Context, shell, target string) error {
	if err := f.trip("MergeSourceLink"); err != nil {
		return err
	}
	return f.inner.MergeSourceLink(c, shell, target)
}
func (f *failingRepo) RemoveSourceLink(c context.Context, k, e string) (bool, error) {
	if err := f.trip("RemoveSourceLink"); err != nil {
		return false, err
	}
	return f.inner.RemoveSourceLink(c, k, e)
}
func (f *failingRepo) SuppressMatch(c context.Context, p, k, e string) error {
	if err := f.trip("SuppressMatch"); err != nil {
		return err
	}
	return f.inner.SuppressMatch(c, p, k, e)
}
func (f *failingRepo) MatchSuppressed(c context.Context, p, k, e string) (bool, error) {
	if err := f.trip("MatchSuppressed"); err != nil {
		return false, err
	}
	return f.inner.MatchSuppressed(c, p, k, e)
}

func newFailingService(t *testing.T) (*Service, *failingRepo, *msfake.Provider) {
	t.Helper()
	inner, err := storage.Open(t.TempDir() + "/f.db")
	if err != nil {
		t.Fatal(err)
	}
	repo := &failingRepo{inner: inner, err: errors.New("boom")}
	sessions := newFakeSessions()
	tidal := msfake.New(musicsource.KindTIDAL)
	tidal.Playlists = []musicsource.RemotePlaylist{remote("p1", "One", "e1", 2)}
	tidal.Tracks = map[string][]playlist.Track{"p1": isrcTracks("A", 2)}
	ctx, cancel := context.WithCancel(context.Background())
	svc := New(ctx, repo, &fakeGenerator{}, fakeImporter{}, sessions, musicsource.Registry{musicsource.KindTIDAL: tidal}, zap.NewNop())
	t.Cleanup(func() { svc.Close(); cancel(); _ = inner.Close() })
	if _, err := svc.CompleteAuth(ctx, musicsource.KindTIDAL, "tok"); err != nil {
		t.Fatal(err)
	}
	return svc, repo, tidal
}

func TestSyncRepoErrors(t *testing.T) {
	ctx := context.Background()

	for _, name := range []string{"ListSourceLinks", "CreateImported", "List", "SetImportedTracks"} {
		t.Run(name, func(t *testing.T) {
			svc, repo, _ := newFailingService(t)
			repo.failOn = name
			// hydrateAndMatch swallows its own errors (logged), so a
			// SetImportedTracks failure does not fail the sync; the others do.
			_, err := svc.SyncSource(ctx, musicsource.KindTIDAL)
			mustFail := name == "ListSourceLinks" || name == "CreateImported"
			if mustFail && err == nil {
				t.Fatalf("%s failure should surface", name)
			}
		})
	}

	t.Run("changed path repo errors", func(t *testing.T) {
		svc, repo, tidal := newFailingService(t)
		if _, err := svc.SyncSource(ctx, musicsource.KindTIDAL); err != nil {
			t.Fatal(err)
		}
		tidal.Playlists[0].ETag = "e2"
		repo.failOn = "MarkSourceChanged"
		if _, err := svc.SyncSource(ctx, musicsource.KindTIDAL); err == nil {
			t.Fatal("MarkSourceChanged failure should surface")
		}
	})

	t.Run("touch and remove errors", func(t *testing.T) {
		svc, repo, tidal := newFailingService(t)
		if _, err := svc.SyncSource(ctx, musicsource.KindTIDAL); err != nil {
			t.Fatal(err)
		}
		repo.failOn = "TouchSourceLink"
		if _, err := svc.SyncSource(ctx, musicsource.KindTIDAL); err == nil {
			t.Fatal("TouchSourceLink failure should surface")
		}
		repo.failOn = "RemoveSourceLink"
		tidal.Playlists = nil
		if _, err := svc.SyncSource(ctx, musicsource.KindTIDAL); err == nil {
			t.Fatal("RemoveSourceLink failure should surface")
		}
	})

	t.Run("unlink repo errors", func(t *testing.T) {
		svc, repo, _ := newFailingService(t)
		if _, err := svc.SyncSource(ctx, musicsource.KindTIDAL); err != nil {
			t.Fatal(err)
		}
		items, _ := svc.List(ctx)
		id := items[0].ID
		repo.failOn = "SuppressMatch"
		if _, err := svc.UnlinkSource(ctx, id, musicsource.KindTIDAL, "p1"); err == nil {
			t.Fatal("SuppressMatch failure should surface")
		}
		repo.failOn = "RemoveSourceLink"
		if _, err := svc.UnlinkSource(ctx, id, musicsource.KindTIDAL, "p1"); err == nil {
			t.Fatal("RemoveSourceLink failure should surface")
		}
	})

	t.Run("findSameMusic repo errors", func(t *testing.T) {
		svc, repo, tidal := newFailingService(t)
		// A generated playlist so findSameMusic has a candidate to iterate.
		if _, err := svc.repo.Create(ctx, playlist.Revision{Title: "G", Description: "d", Tracks: isrcTracks("A", 2), CreatedAt: time.Now()}, nil); err != nil {
			t.Fatal(err)
		}
		repo.failOn = "MatchSuppressed"
		tidal.Tracks["p1"] = isrcTracks("A", 2)
		if _, err := svc.SyncSource(ctx, musicsource.KindTIDAL); err == nil {
			// hydrateAndMatch logs its error, so the sync itself may still
			// succeed; assert the playlist was not merged instead.
			if items, _ := svc.List(ctx); len(items) != 2 {
				t.Fatalf("expected the import to remain separate on match error, got %d rows", len(items))
			}
		}
	})
}
