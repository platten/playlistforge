// Package connections stores one streaming-service session per service. It
// prefers the OS keyring and falls back to a permission-restricted file under
// the application directory when the keyring is unavailable (a common case on
// headless Linux and WSL, where no Secret Service is running). Streaming
// sessions are revocable, lower-stakes tokens than the OpenAI key, so the file
// fallback is automatic rather than consent-gated.
package connections

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const keyringAccount = "session"

func keyringService(name string) string { return "com.playlistforge." + name }

// ErrNotConnected indicates that no session is stored for a service.
var ErrNotConnected = errors.New("streaming service is not connected")

// Store reads and writes per-service session blobs. Function fields isolate the
// OS keyring in tests; fileDir is the fallback location.
type Store struct {
	fileDir   string
	keyGet    func(service, account string) (string, error)
	keySet    func(service, account, value string) error
	keyDelete func(service, account string) error
}

// New returns a Store backed by the real OS keyring, falling back to files
// under configDir/connections.
func New(configDir string) *Store {
	return &Store{
		fileDir:   filepath.Join(configDir, "connections"),
		keyGet:    keyring.Get,
		keySet:    keyring.Set,
		keyDelete: keyring.Delete,
	}
}

// Get returns the stored session blob for name, or ErrNotConnected. The keyring
// is tried first; on a miss — including a keyring that is simply unavailable —
// the fallback file is consulted. Only "here is the session" or "not connected"
// are ever reported, so a keyringless host does not surface an error on every
// read.
func (s *Store) Get(name string) ([]byte, error) {
	if value, err := s.keyGet(keyringService(name), keyringAccount); err == nil && strings.TrimSpace(value) != "" {
		return []byte(value), nil
	}
	if blob, err := s.readFile(name); err == nil && len(blob) > 0 {
		return blob, nil
	}
	return nil, ErrNotConnected
}

// Set stores blob as the session for name, replacing any previous value. It
// uses the keyring when that write succeeds and the fallback file otherwise;
// only if both fail does it return an error.
func (s *Store) Set(name string, blob []byte) error {
	if len(blob) == 0 {
		return errors.New("refusing to store an empty session")
	}
	keyErr := s.keySet(keyringService(name), keyringAccount, string(blob))
	if keyErr == nil {
		_ = s.removeFile(name) // don't let a stale file shadow the keyring
		return nil
	}
	if fileErr := s.writeFile(name, blob); fileErr != nil {
		return fmt.Errorf("store %s session: keyring: %v; file: %w", name, keyErr, fileErr)
	}
	return nil
}

// Delete removes the stored session for name from both stores. A missing entry
// is not an error, nor is a keyring that cannot service the request.
func (s *Store) Delete(name string) error {
	_ = s.keyDelete(keyringService(name), keyringAccount)
	if err := s.removeFile(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s session: %w", name, err)
	}
	return nil
}

// Has reports whether a session is stored for name.
func (s *Store) Has(name string) bool {
	_, err := s.Get(name)
	return err == nil
}

// --- file fallback ---------------------------------------------------------

func (s *Store) filePath(name string) string {
	return filepath.Join(s.fileDir, safeName(name)+".json")
}

func (s *Store) readFile(name string) ([]byte, error) {
	if s.fileDir == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(s.filePath(name))
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Session json.RawMessage `json:"session"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil || len(wrapper.Session) == 0 {
		return nil, nil
	}
	return wrapper.Session, nil
}

func (s *Store) writeFile(name string, blob []byte) error {
	if s.fileDir == "" {
		return errors.New("no fallback directory configured")
	}
	if err := os.MkdirAll(s.fileDir, 0o700); err != nil {
		return err
	}
	wrapped, err := json.Marshal(struct {
		Session json.RawMessage `json:"session"`
	}{Session: json.RawMessage(blob)})
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath(name), wrapped, 0o600)
}

func (s *Store) removeFile(name string) error {
	if s.fileDir == "" {
		return nil
	}
	return os.Remove(s.filePath(name))
}

// safeName keeps a service name to a single, filesystem-safe path segment.
func safeName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
	if name == "" {
		return "service"
	}
	return name
}
