// Package credentials stores the OpenAI API key in the platform keyring and
// provides an explicit, permission-restricted file fallback.
package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const service = "com.playlistforge.openai"
const account = "openai_api_key"
const openAIKeyEnvironment = "OPENAI_API_KEY"

// ErrNotConfigured indicates that neither the keyring nor fallback file has a key.
var ErrNotConfigured = errors.New("OpenAI API key is not configured")

// Status is the non-secret credential state returned to the browser.
type Status struct {
	Configured bool   `json:"configured"`
	Storage    string `json:"storage"`
	ReadOnly   bool   `json:"readOnly,omitempty"`
}

// Store reads and writes the API key. Function fields isolate the operating
// system keyring in tests without weakening production behavior.
type Store struct {
	configPath string
	keyGet     func(string, string) (string, error)
	keySet     func(string, string, string) error
	keyDelete  func(string, string) error
	envGet     func(string) string
}

type diskConfig struct {
	OpenAIKey string `json:"openaiApiKey"`
}

// New creates a Store whose optional fallback file is under configDir.
func New(configDir string) *Store {
	return &Store{
		configPath: filepath.Join(configDir, "config.json"),
		keyGet:     keyring.Get,
		keySet:     keyring.Set,
		keyDelete:  keyring.Delete,
		envGet:     os.Getenv,
	}
}

// Status reports whether a key exists and where it is stored, never its value.
func (s *Store) Status() Status {
	if strings.TrimSpace(s.envGet(openAIKeyEnvironment)) != "" {
		return Status{Configured: true, Storage: "environment", ReadOnly: true}
	}
	if value, err := s.keyGet(service, account); err == nil && strings.TrimSpace(value) != "" {
		return Status{Configured: true, Storage: "keyring"}
	}
	if cfg, err := s.readDisk(); err == nil && strings.TrimSpace(cfg.OpenAIKey) != "" {
		return Status{Configured: true, Storage: "config"}
	}
	return Status{Storage: "none"}
}

// Get returns the environment value first, followed by the keyring and config
// file. Environment precedence makes container deployment deterministic without
// copying a secret into the image or the application data volume.
func (s *Store) Get() (string, error) {
	if value := strings.TrimSpace(s.envGet(openAIKeyEnvironment)); value != "" {
		return value, nil
	}
	if value, err := s.keyGet(service, account); err == nil && strings.TrimSpace(value) != "" {
		return value, nil
	}
	cfg, err := s.readDisk()
	if err == nil && strings.TrimSpace(cfg.OpenAIKey) != "" {
		return cfg.OpenAIKey, nil
	}
	return "", ErrNotConfigured
}

// Set prefers the platform keyring. It writes the fallback file only when the
// caller has explicitly opted in and the keyring write fails.
func (s *Store) Set(value string, allowPlaintext bool) (Status, error) {
	if strings.TrimSpace(s.envGet(openAIKeyEnvironment)) != "" {
		return Status{}, fmt.Errorf("API key is managed by %s and cannot be changed in the web interface", openAIKeyEnvironment)
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 512 {
		return Status{}, errors.New("API key is empty or too long")
	}
	if err := s.keySet(service, account, value); err == nil {
		_ = os.Remove(s.configPath)
		return Status{Configured: true, Storage: "keyring"}, nil
	} else if !allowPlaintext {
		return Status{}, fmt.Errorf("OS credential store unavailable: %w", err)
	}
	if err := s.writeDisk(diskConfig{OpenAIKey: value}); err != nil {
		return Status{}, err
	}
	return Status{Configured: true, Storage: "config"}, nil
}

// Delete removes both possible copies so changing storage modes cannot leave a
// stale credential behind.
func (s *Store) Delete() error {
	if strings.TrimSpace(s.envGet(openAIKeyEnvironment)) != "" {
		return fmt.Errorf("API key is managed by %s and cannot be removed in the web interface", openAIKeyEnvironment)
	}
	errKeyring := s.keyDelete(service, account)
	errDisk := os.Remove(s.configPath)
	if errKeyring != nil && !errors.Is(errKeyring, keyring.ErrNotFound) && errDisk != nil && !errors.Is(errDisk, os.ErrNotExist) {
		return fmt.Errorf("delete keyring credential: %w", errKeyring)
	}
	return nil
}

func (s *Store) readDisk() (diskConfig, error) {
	if err := rejectSymlink(s.configPath); err != nil {
		return diskConfig{}, err
	}
	data, err := os.ReadFile(s.configPath)
	if err != nil {
		return diskConfig{}, err
	}
	var cfg diskConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return diskConfig{}, fmt.Errorf("decode config: %w", err)
	}
	return cfg, nil
}

func (s *Store) writeDisk(cfg diskConfig) error {
	if err := os.MkdirAll(filepath.Dir(s.configPath), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(s.configPath), 0o700); err != nil {
		return fmt.Errorf("restrict config directory: %w", err)
	}
	if err := rejectSymlink(s.configPath); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	// Write, sync, and atomically replace from the same directory so a crash
	// cannot leave a partially written JSON credential file.
	temp, err := os.CreateTemp(filepath.Dir(s.configPath), "config-*.tmp")
	if err != nil {
		return fmt.Errorf("create config temp file: %w", err)
	}
	name := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(name)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict config permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := replaceFile(name, s.configPath); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func rejectSymlink(path string) error {
	// Lstat prevents a local symlink from redirecting a secret outside the
	// application configuration directory.
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect config file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to use a symlink as the credential config file")
	}
	if !info.Mode().IsRegular() {
		return errors.New("credential config path is not a regular file")
	}
	return nil
}
