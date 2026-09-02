package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// setupDiagnosticLog points slog at <UserConfigDir>/playlist-forge/playlist-forge.log
// (as well as stderr) so the streaming sign-in and browser-open traces are
// recoverable on a packaged Windows build, which has no console. Best-effort:
// on any failure it leaves the default stderr handler in place. Returns a
// closer for the file, or nil.
func setupDiagnosticLog(applicationDir string) io.Closer {
	root, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	if applicationDir == "" {
		applicationDir = "playlist-forge"
	}
	dir := filepath.Join(root, applicationDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, "playlist-forge.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(
		io.MultiWriter(os.Stderr, f),
		&slog.HandlerOptions{Level: slog.LevelInfo},
	)))
	slog.Info("playlist-forge starting", "version", version)
	return f
}
