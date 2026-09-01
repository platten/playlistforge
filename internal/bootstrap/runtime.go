// Package bootstrap composes the application adapters shared by the browser
// and desktop entrypoints.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go.uber.org/zap"

	"playlistforge/internal/app"
	"playlistforge/internal/credentials"
	"playlistforge/internal/logging"
	"playlistforge/internal/openaiapi"
	"playlistforge/internal/soundiiz"
	"playlistforge/internal/storage"
)

// Options contains the process-specific inputs needed to start Playlist Forge.
type Options struct {
	Context        context.Context
	ConfigDir      string
	LogFormat      string
	LogLevel       string
	ApplicationDir string
}

// Runtime owns the long-lived application resources used by a presentation
// adapter. Close must be called before its process exits.
type Runtime struct {
	Service   *app.Service
	Keys      *credentials.Store
	Validator *openaiapi.Validator
	Logger    *zap.Logger
	ConfigDir string

	repo      *storage.Repository
	closeOnce sync.Once
	closeErr  error
}

// New constructs the complete application runtime.
func New(options Options) (*Runtime, error) {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	logFormat := options.LogFormat
	if logFormat == "" {
		logFormat = "console"
	}
	logLevel := options.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}
	logger, err := logging.New(logFormat, logLevel)
	if err != nil {
		return nil, fmt.Errorf("configure logging: %w", err)
	}
	configDir, err := ResolveConfigDir(options.ConfigDir, options.ApplicationDir)
	if err != nil {
		_ = logger.Sync()
		return nil, err
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		_ = logger.Sync()
		return nil, fmt.Errorf("create application directory: %w", err)
	}
	repo, err := storage.Open(filepath.Join(configDir, "playlists.db"))
	if err != nil {
		_ = logger.Sync()
		return nil, err
	}
	keys := credentials.New(configDir)
	service := app.New(ctx, repo, openaiapi.New(keys, logger), soundiiz.New(), logger)
	return &Runtime{
		Service:   service,
		Keys:      keys,
		Validator: openaiapi.NewValidator(),
		Logger:    logger,
		ConfigDir: configDir,
		repo:      repo,
	}, nil
}

// Close stops background work before closing persistence.
func (r *Runtime) Close() error {
	r.closeOnce.Do(func() {
		r.Service.Close()
		r.closeErr = errors.Join(r.closeErr, r.repo.Close())
		_ = r.Logger.Sync()
	})
	return r.closeErr
}

// ResolveConfigDir returns an absolute override or the OS-standard directory.
func ResolveConfigDir(override, applicationDir string) (string, error) {
	if override != "" {
		absolute, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("resolve application directory: %w", err)
		}
		return filepath.Clean(absolute), nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	if applicationDir == "" {
		applicationDir = "playlist-forge"
	}
	return filepath.Join(root, applicationDir), nil
}
