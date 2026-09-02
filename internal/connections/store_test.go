package connections

// Tests for the per-service session store: the keyring round-trip, the
// permission-restricted file fallback used when the keyring is unavailable,
// keyring taking precedence over a stale file, blank/empty handling, and that a
// keyringless host never surfaces a raw error from Get/Delete. The OS keyring is
// replaced with in-memory (or always-failing) function fields.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

// newKeyringStore returns a Store whose keyring is an in-memory map and whose
// file fallback lives in a temp dir.
func newKeyringStore(t *testing.T) (*Store, map[string]string) {
	t.Helper()
	data := map[string]string{}
	key := func(service, account string) string { return service + "\x00" + account }
	return &Store{
		fileDir: t.TempDir(),
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

// newKeyringlessStore returns a Store whose keyring always fails, as on WSL or a
// headless Linux box with no Secret Service.
func newKeyringlessStore(t *testing.T) *Store {
	t.Helper()
	down := errors.New("dial unix /run/user/1000/bus: connect: no such file or directory")
	return &Store{
		fileDir:   t.TempDir(),
		keyGet:    func(string, string) (string, error) { return "", down },
		keySet:    func(string, string, string) error { return down },
		keyDelete: func(string, string) error { return down },
	}
}

func TestKeyringRoundTrip(t *testing.T) {
	store, data := newKeyringStore(t)

	if _, err := store.Get("tidal"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("get missing: %v", err)
	}
	if store.Has("tidal") {
		t.Fatal("has missing")
	}
	if err := store.Set("tidal", nil); err == nil {
		t.Fatal("empty blob accepted")
	}
	if err := store.Set("tidal", []byte(`{"token":"abc"}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, ok := data["com.playlistforge.tidal\x00session"]; !ok {
		t.Fatal("keyring was not written")
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

func TestFileFallbackWhenKeyringUnavailable(t *testing.T) {
	store := newKeyringlessStore(t)

	if _, err := store.Get("qobuz"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("get missing on keyringless host: %v", err)
	}
	if err := store.Set("qobuz", []byte(`{"token":"xyz"}`)); err != nil {
		t.Fatalf("set should fall back to a file: %v", err)
	}

	path := filepath.Join(store.fileDir, "qobuz.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("fallback file missing: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("fallback file mode = %o, want 600", perm)
	}

	blob, err := store.Get("qobuz")
	if err != nil || string(blob) != `{"token":"xyz"}` {
		t.Fatalf("get from file: %q %v", blob, err)
	}
	if !store.Has("qobuz") {
		t.Fatal("has after file set")
	}
	if err := store.Delete("qobuz"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fallback file survived delete: %v", err)
	}
	if _, err := store.Get("qobuz"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestKeyringTakesPrecedenceAndClearsStaleFile(t *testing.T) {
	store, data := newKeyringStore(t)

	// A leftover file from a previous keyringless run.
	if err := store.writeFile("tidal", []byte(`{"token":"old-file"}`)); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	// A successful keyring write must win and remove that file.
	if err := store.Set("tidal", []byte(`{"token":"new-keyring"}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := os.Stat(store.filePath("tidal")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale file not cleared: %v", err)
	}
	blob, err := store.Get("tidal")
	if err != nil || string(blob) != `{"token":"new-keyring"}` {
		t.Fatalf("get: %q %v", blob, err)
	}
	_ = data
}

func TestBlankKeyringValueFallsThrough(t *testing.T) {
	store, data := newKeyringStore(t)
	data["com.playlistforge.qobuz\x00session"] = "   "
	if _, err := store.Get("qobuz"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("blank keyring value should be not-connected: %v", err)
	}
}

func TestSetReportsWhenBothStoresFail(t *testing.T) {
	store := newKeyringlessStore(t)
	store.fileDir = "" // no fallback location either
	if err := store.Set("tidal", []byte("x")); err == nil {
		t.Fatal("expected an error when neither store can hold the session")
	}
}
