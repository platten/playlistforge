// Command playlist-forge runs Playlist Forge as a native Wails v3 desktop app.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"playlistforge/internal/bootstrap"
	"playlistforge/internal/desktop"
)

//go:embed all:internal/webui/dist
var desktopAssets embed.FS

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func run() (runErr error) {
	runtime, err := bootstrap.New(bootstrap.Options{
		Context:        context.Background(),
		ApplicationDir: "playlist-forge",
	})
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := runtime.Close(); runErr == nil {
			runErr = closeErr
		}
	}()

	assets, err := fs.Sub(desktopAssets, "internal/webui/dist")
	if err != nil {
		return fmt.Errorf("open desktop assets: %w", err)
	}

	// Wails v3 has no per-call context on the frontend transport, so the bound
	// service keeps the process context captured here. External URLs are opened
	// through the running application's browser manager.
	desktopAPI := desktop.New(
		context.Background(),
		runtime.Service,
		runtime.Keys,
		runtime.Validator,
		func(raw string) { _ = openInBrowser(raw) },
		runAuth,
	)

	app := application.New(application.Options{
		Name:        "Playlist Forge",
		Description: "Local-first AI playlist curation",
		Services: []application.Service{
			application.NewService(desktopAPI),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
		// Captures posted by the streaming sign-in window (see authwindow.go).
		RawMessageHandler: handleRawMessage,
		LogLevel:          slog.LevelWarn,
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Playlist Forge",
		Width:            1280,
		Height:           800,
		MinWidth:         900,
		MinHeight:        640,
		BackgroundColour: application.NewRGB(11, 12, 10),
		URL:              "/",
	})

	return app.Run()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "playlist-forge:", err)
		os.Exit(1)
	}
}
