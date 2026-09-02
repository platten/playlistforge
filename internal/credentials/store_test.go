package credentials

// Tests for key-storage precedence and lifecycle: OPENAI_API_KEY wins and is
// reported read-only; keyring get / set / delete; the opt-in config-file
// fallback with a 0600 file, a 0700 directory, and an atomic replace; and
// refusal to read or write through a symlinked or non-regular config path.
// Function fields replace the real OS keyring so no secret leaves the test.

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	store := New(t.TempDir())
	store.keyGet = func(string, string) (string, error) { return "", keyring.ErrNotFound }
	store.keySet = func(string, string, string) error { return errors.New("unavailable") }
	store.keyDelete = func(string, string) error { return keyring.ErrNotFound }
	store.envGet = func(string) string { return "" }
	return store
}

func TestEnvironmentTakesPrecedenceAndIsReadOnly(t *testing.T) {
	store := testStore(t)
	store.envGet = func(name string) string {
		if name != openAIKeyEnvironment {
			t.Fatalf("environment name = %q", name)
		}
		return "  sk-from-environment  "
	}
	store.keyGet = func(string, string) (string, error) { return "sk-from-keyring", nil }
	status := store.Status()
	if !status.Configured || status.Storage != "environment" || !status.ReadOnly {
		t.Fatalf("status = %#v", status)
	}
	if got, err := store.Get(); err != nil || got != "sk-from-environment" {
		t.Fatalf("get = %q, %v", got, err)
	}
	if _, err := store.Set("sk-replacement", true); err == nil || !strings.Contains(err.Error(), openAIKeyEnvironment) {
		t.Fatalf("set error = %v", err)
	}
	if err := store.Delete(); err == nil || !strings.Contains(err.Error(), openAIKeyEnvironment) {
		t.Fatalf("delete error = %v", err)
	}
}

func TestKeyringLifecycle(t *testing.T) {
	store := testStore(t)
	var saved string
	store.keySet = func(serviceName, accountName, value string) error {
		if serviceName != service || accountName != account {
			t.Fatal("wrong keyring names")
		}
		saved = value
		return nil
	}
	store.keyGet = func(string, string) (string, error) { return saved, nil }
	status, err := store.Set("  sk-secret  ", false)
	if err != nil || !status.Configured || status.Storage != "keyring" || saved != "sk-secret" {
		t.Fatalf("set = %#v, %v", status, err)
	}
	if status := store.Status(); !status.Configured || status.Storage != "keyring" {
		t.Fatalf("status = %#v", status)
	}
	if got, err := store.Get(); err != nil || got != saved {
		t.Fatalf("get = %q, %v", got, err)
	}
	deleted := false
	store.keyDelete = func(string, string) error { deleted = true; return nil }
	if err := store.Delete(); err != nil || !deleted {
		t.Fatalf("delete = %v", err)
	}
}

func TestFallbackLifecycle(t *testing.T) {
	store := testStore(t)
	if _, err := store.Set("", true); err == nil {
		t.Fatal("accepted empty key")
	}
	if _, err := store.Set(strings.Repeat("x", 513), true); err == nil {
		t.Fatal("accepted long key")
	}
	if _, err := store.Set("sk-x", false); err == nil || !strings.Contains(err.Error(), "credential store") {
		t.Fatalf("error = %v", err)
	}
	status, err := store.Set("sk-fallback", true)
	if err != nil || status.Storage != "config" {
		t.Fatalf("set = %#v, %v", status, err)
	}
	if _, err := store.Set("sk-fallback-updated", true); err != nil {
		t.Fatalf("replace config = %v", err)
	}
	if status := store.Status(); !status.Configured || status.Storage != "config" {
		t.Fatalf("status = %#v", status)
	}
	if got, err := store.Get(); err != nil || got != "sk-fallback-updated" {
		t.Fatalf("get = %q, %v", got, err)
	}
	info, err := os.Stat(store.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("permissions too broad: %v", info.Mode())
	}
	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	if status := store.Status(); status.Configured || status.Storage != "none" {
		t.Fatalf("status = %#v", status)
	}
	if _, err := store.Get(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("get error = %v", err)
	}
}

func TestDiskAndDeleteErrors(t *testing.T) {
	store := testStore(t)
	if err := os.WriteFile(store.configPath, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.readDisk(); err == nil {
		t.Fatal("expected invalid json")
	}
	store.keyDelete = func(string, string) error { return errors.New("delete failed") }
	nonEmpty := filepath.Join(t.TempDir(), "nonempty")
	if err := os.Mkdir(nonEmpty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "child"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.configPath = nonEmpty
	if err := store.Delete(); err == nil {
		t.Fatal("expected delete error")
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.configPath = filepath.Join(blocked, "config.json")
	if _, err := store.Set("sk-x", true); err == nil {
		t.Fatal("expected directory error")
	}
}

func TestRejectSymlinkAndNonRegular(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err == nil {
		if err := rejectSymlink(link); err == nil {
			t.Fatal("accepted symlink")
		}
	}
	if err := rejectSymlink(dir); err == nil {
		t.Fatal("accepted directory")
	}
	if err := rejectSymlink(filepath.Join(dir, "missing")); err != nil {
		t.Fatal(err)
	}
}
