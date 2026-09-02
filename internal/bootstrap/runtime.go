// Package bootstrap composes the application adapters used by the desktop
// entrypoint.
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
	"playlistforge/internal/connections"
	"playlistforge/internal/credentials"
	"playlistforge/internal/logging"
	"playlistforge/internal/musicsource"
	"playlistforge/internal/musicsource/qobuz"
	"playlistforge/internal/musicsource/tidal"
	"playlistforge/internal/openaiapi"
	"playlistforge/internal/soundiiz"
	"playlistforge/internal/storage"
)

// Options contains the process-specific inputs needed to start Playlist Forge.
type Options struct {
	Context        context.Context
	ApplicationDir string
}

// Runtime owns the long-lived application resources used by a presentation
// adapter. Close must be called before its process exits.
type Runtime struct {
	Service   *app.Service
	Keys      *credentials.Store
	Validator *openaiapi.Validator

	repo      *storage.Repository
	logger    *zap.Logger
	closeOnce sync.Once
	closeErr  error
}

// New constructs the complete application runtime.
func New(options Options) (*Runtime, error) {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	logger, err := logging.New("console", "info")
	if err != nil {
		return nil, fmt.Errorf("configure logging: %w", err)
	}
	configDir, err := resolveConfigDir(options.ApplicationDir)
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
	// Reverse-engineered adapters for the two services Playlist Forge can import
	// from. Both use community client credentials and undocumented endpoints.
	sources := musicsource.Registry{
		musicsource.KindTIDAL: tidal.New(),
		musicsource.KindQobuz: qobuz.New(),
	}
	service := app.New(ctx, repo, openaiapi.New(keys, logger), soundiiz.New(), connections.New(), sources, logger)
	return &Runtime{
		Service:   service,
		Keys:      keys,
		Validator: openaiapi.NewValidator(),
		repo:      repo,
		logger:    logger,
	}, nil
}

// Close stops background work before closing persistence.
func (r *Runtime) Close() error {
	r.closeOnce.Do(func() {
		r.Service.Close()
		r.closeErr = errors.Join(r.closeErr, r.repo.Close())
		_ = r.logger.Sync()
	})
	return r.closeErr
}

// resolveConfigDir returns the OS-standard application directory.
func resolveConfigDir(applicationDir string) (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	if applicationDir == "" {
		applicationDir = "playlist-forge"
	}
	return filepath.Join(root, applicationDir), nil
}
