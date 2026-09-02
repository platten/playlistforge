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
	"testing"

	"go.uber.org/zap"

	"playlistforge/internal/app"
	"playlistforge/internal/credentials"
	"playlistforge/internal/musicsource"
)

type fakeKeys struct {
	status credentials.Status
	value  string
}

func (f *fakeKeys) Status() credentials.Status { return f.status }
func (f *fakeKeys) Set(value string, _ bool) (credentials.Status, error) {
	f.value = value
	return credentials.Status{Configured: true, Storage: "keyring"}, nil
}
func (f *fakeKeys) Delete() error { f.value = ""; return nil }

type fakeValidator struct{ err error }

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
