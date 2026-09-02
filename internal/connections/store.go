// Package connections stores one streaming-service session per service in the
// OS keyring. Unlike internal/credentials it has no environment or config-file
// fallback: streaming import is optional, so when the keyring is unavailable
// connecting is simply disabled rather than degraded to a plaintext file.
package connections

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

const keyringAccount = "session"

func keyringService(name string) string { return "com.playlistforge." + name }

// ErrNotConnected indicates that no session is stored for a service.
var ErrNotConnected = errors.New("streaming service is not connected")

// Store reads and writes per-service session blobs. Function fields isolate the
// OS keyring in tests.
type Store struct {
	keyGet    func(service, account string) (string, error)
	keySet    func(service, account, value string) error
	keyDelete func(service, account string) error
}

// New returns a Store backed by the real OS keyring.
func New() *Store {
	return &Store{keyGet: keyring.Get, keySet: keyring.Set, keyDelete: keyring.Delete}
}

// Get returns the stored session blob for name, or ErrNotConnected.
func (s *Store) Get(name string) ([]byte, error) {
	value, err := s.keyGet(keyringService(name), keyringAccount)
	if errors.Is(err, keyring.ErrNotFound) || (err == nil && strings.TrimSpace(value) == "") {
		return nil, ErrNotConnected
	}
	if err != nil {
		return nil, fmt.Errorf("read %s session: %w", name, err)
	}
	return []byte(value), nil
}

// Set stores blob as the session for name, replacing any previous value.
func (s *Store) Set(name string, blob []byte) error {
	if len(blob) == 0 {
		return errors.New("refusing to store an empty session")
	}
	if err := s.keySet(keyringService(name), keyringAccount, string(blob)); err != nil {
		return fmt.Errorf("store %s session: %w", name, err)
	}
	return nil
}

// Delete removes the stored session for name. A missing entry is not an error.
func (s *Store) Delete(name string) error {
	err := s.keyDelete(keyringService(name), keyringAccount)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("remove %s session: %w", name, err)
	}
	return nil
}

// Has reports whether a session is stored for name.
func (s *Store) Has(name string) bool {
	_, err := s.Get(name)
	return err == nil
}
