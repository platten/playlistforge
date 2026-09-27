package desktop

// Tests for the Wails-facing adapter: assembly of the Config contract and
// pass-through of credential status, and the OpenExternalURL allow-list, which
// must accept only the Soundiiz handoff origin and the exact OpenAI billing
// page and reject everything else (wrong scheme, host, path, userinfo, query,
// or fragment). Fakes stand in for the credential store, key validator, and the
// URL opener.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"playlistforge/internal/app"
	"playlistforge/internal/credentials"
	"playlistforge/internal/deezer"
	"playlistforge/internal/musicsource"
	"playlistforge/internal/playlist"
	"playlistforge/internal/storage"
)

type fakeKeys struct {
	status credentials.Status
	value  string
	err    error
}

func (f *fakeKeys) Get() (string, error) { return f.value, f.err }

func (f *fakeKeys) Status() credentials.Status { return f.status }
func (f *fakeKeys) Set(value string, _ bool) (credentials.Status, error) {
	f.value = value
	return credentials.Status{Configured: true, Storage: "keyring"}, nil
}
func (f *fakeKeys) Delete() error { f.value = ""; return nil }

type fakeValidator struct{ err error }

func (f fakeValidator) Models(context.Context, string) ([]string, error) {
	return []string{"gpt-6-sol", "gpt-6-luna"}, f.err
}

func (f fakeValidator) Validate(context.Context, string) error { return f.err }

func TestConfigAndCredentials(t *testing.T) {
	keys := &fakeKeys{status: credentials.Status{Configured: true, Storage: "keyring"}}
	api := New(context.Background(), nil, keys, fakeValidator{}, nil, nil)
	config := api.Config()
	if !config.Credential.Configured || config.Model == "" || len(config.TrackCounts) != 6 || config.Pricing.Version == "" {
		t.Fatalf("unexpected config: %+v", config)
	}
	status, err := api.SaveKey("sk-test", false)
	if err != nil || !status.Configured || keys.value != "sk-test" {
		t.Fatalf("save key: %+v, %v", status, err)
	}
	if err := api.DeleteKey(); err != nil || keys.value != "" {
		t.Fatalf("delete key: %v", err)
	}
	api.validator = fakeValidator{err: errors.New("invalid key")}
	if _, err := api.SaveKey("bad", false); err == nil {
		t.Fatal("invalid key was accepted")
	}
}

func TestOpenExternalURL(t *testing.T) {
	var opened string
	api := New(context.Background(), nil, &fakeKeys{}, fakeValidator{}, func(raw string) { opened = raw }, nil)
	trusted := "https://soundiiz.com/go/import-playlist/token"
	if err := api.OpenExternalURL(trusted); err != nil || opened != trusted {
		t.Fatalf("trusted URL: %q, %v", opened, err)
	}
	billing := "https://platform.openai.com/settings/organization/billing/overview"
	if err := api.OpenExternalURL(billing); err != nil || opened != billing {
		t.Fatalf("billing URL: %q, %v", opened, err)
	}
	for _, raw := range []string{
		"http://soundiiz.com/go/import-playlist/token",
		"https://example.com/go/import-playlist/token",
		"https://soundiiz.com/other/token",
		"https://user@soundiiz.com/go/import-playlist/token",
		"https://platform.openai.com/settings/organization/billing/overview?next=evil",
		"https://platform.openai.com/settings/organization/billing/overview#other",
		"https://platform.openai.com/settings/organization/billing",
		"https://user@platform.openai.com/settings/organization/billing/overview",
	} {
		if err := api.OpenExternalURL(raw); err == nil {
			t.Fatalf("untrusted URL accepted: %s", raw)
		}
	}
	api.openURL = nil
	if err := api.OpenExternalURL(trusted); err == nil {
		t.Fatal("missing handler was accepted")
	}
}

func TestStreamingMethods(t *testing.T) {
	svc := app.New(context.Background(), nil, nil, nil, nil, nil, zap.NewNop())
	t.Cleanup(svc.Close)

	api := New(context.Background(), svc, &fakeKeys{}, fakeValidator{}, nil, nil)
	if status := api.Connections(); len(status) != 2 || status[0].Connected || status[0].Available {
		t.Fatalf("connections: %+v", status)
	}
	if status := api.CheckConnections(); len(status) != 2 || status[0].Connected || status[0].NeedsReauth {
		t.Fatalf("check connections: %+v", status)
	}
	if _, err := api.ConnectService("tidal"); err == nil {
		t.Fatal("connect without a sign-in window should fail")
	}

	api.runAuth = func(musicsource.AuthRequest) (string, error) { return "captured", nil }
	if _, err := api.ConnectService("tidal"); err == nil {
		t.Fatal("connect with no provider registered should fail")
	}
	if err := api.DisconnectService("tidal"); err == nil {
		t.Fatal("disconnect with no session store should fail")
	}
	if _, err := api.SyncSource("tidal"); err == nil {
		t.Fatal("sync without a session should fail")
	}
}

func TestListModels(t *testing.T) {
	keys := &fakeKeys{value: "key"}
	api := New(context.Background(), nil, keys, fakeValidator{}, nil, nil)
	models, err := api.ListModels()
	if err != nil || len(models) != 2 {
		t.Fatalf("models=%v err=%v", models, err)
	}
	keys.err = errors.New("no key")
	if _, err := api.ListModels(); err == nil {
		t.Fatal("expected missing key error")
	}
	keys.err = nil
	api.validator = fakeValidator{err: errors.New("offline")}
	if _, err := api.ListModels(); err == nil {
		t.Fatal("expected provider error")
	}
}

func TestTrackPreviewOnlyLooksUpSavedTrack(t *testing.T) {
	ctx := context.Background()
	repo, err := storage.Open(filepath.Join(t.TempDir(), "playlists.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	_, err = repo.Create(ctx, playlist.Revision{ID: "revision", PlaylistID: "playlist", Title: "Preview", TrackTarget: 1, Tracks: []playlist.Track{{ID: "track", Title: "Song", Artists: []string{"Artist"}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := app.New(ctx, repo, nil, nil, nil, nil, zap.NewNop())
	t.Cleanup(svc.Close)
	api := New(ctx, svc, &fakeKeys{}, fakeValidator{}, nil, nil)
	calls := 0
	api.preview = func(_ context.Context, track playlist.Track) (deezer.Preview, error) {
		calls++
		if track.Title != "Song" {
			t.Fatalf("track=%+v", track)
		}
		return deezer.Preview{URL: "https://cdnt-preview.dzcdn.net/song.mp3"}, nil
	}
	got, err := api.TrackPreview("playlist", "track")
	if err != nil || got.URL == "" || calls != 1 {
		t.Fatalf("preview=%+v err=%v calls=%d", got, err, calls)
	}
	if _, err := api.TrackPreview("playlist", "missing"); err == nil || calls != 1 {
		t.Fatalf("accepted missing track: %v", err)
	}
	if _, err := api.TrackPreview("missing", "track"); err == nil || calls != 1 {
		t.Fatalf("accepted missing playlist: %v", err)
	}
}
