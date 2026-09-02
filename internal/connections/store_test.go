package connections

// Tests for the per-service session store: get / set / delete / has round-trip,
// ErrNotConnected for a missing or blank entry, a missing entry not being a
// delete error, refusal to store an empty blob, and keyring error wrapping.
// The OS keyring is replaced with in-memory function fields.

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func newTestStore() (*Store, map[string]string) {
	data := map[string]string{}
	key := func(service, account string) string { return service + "\x00" + account }
	return &Store{
		keyGet: func(service, account string) (string, error) {
			v, ok := data[key(service, account)]
			if !ok {
				return "", keyring.ErrNotFound
			}
			return v, nil
		},
		keySet: func(service, account, value string) error {
			data[key(service, account)] = value
			return nil
		},
		keyDelete: func(service, account string) error {
			if _, ok := data[key(service, account)]; !ok {
				return keyring.ErrNotFound
			}
			delete(data, key(service, account))
			return nil
		},
	}, data
}

func TestStoreLifecycle(t *testing.T) {
	store, _ := newTestStore()

	if _, err := store.Get("tidal"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("get missing: %v", err)
	}
	if store.Has("tidal") {
		t.Fatal("has missing")
	}
	if err := store.Set("tidal", []byte("")); err == nil {
		t.Fatal("empty blob accepted")
	}
	if err := store.Set("tidal", []byte(`{"token":"abc"}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	blob, err := store.Get("tidal")
	if err != nil || string(blob) != `{"token":"abc"}` {
		t.Fatalf("get: %q %v", blob, err)
	}
	if !store.Has("tidal") {
		t.Fatal("has after set")
	}
	if err := store.Delete("tidal"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := store.Delete("tidal"); err != nil {
		t.Fatalf("delete missing should be nil: %v", err)
	}
	if store.Has("tidal") {
		t.Fatal("has after delete")
	}
}

func TestStoreBlankAndErrors(t *testing.T) {
	store, data := newTestStore()
	data["com.playlistforge.qobuz\x00session"] = "   "
	if _, err := store.Get("qobuz"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("blank should be not-connected: %v", err)
	}

	boom := errors.New("keyring exploded")
	store.keyGet = func(string, string) (string, error) { return "", boom }
	store.keySet = func(string, string, string) error { return boom }
	store.keyDelete = func(string, string) error { return boom }
	if _, err := store.Get("tidal"); !errors.Is(err, boom) {
		t.Fatalf("get error not wrapped: %v", err)
	}
	if err := store.Set("tidal", []byte("x")); !errors.Is(err, boom) {
		t.Fatalf("set error not wrapped: %v", err)
	}
	if err := store.Delete("tidal"); !errors.Is(err, boom) {
		t.Fatalf("delete error not wrapped: %v", err)
	}
}
