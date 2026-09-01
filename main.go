// Command playlist-forge-desktop runs Playlist Forge as a native Wails app.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"playlistforge/internal/bootstrap"
	"playlistforge/internal/desktop"
)

//go:embed all:internal/webui/dist
var desktopAssets embed.FS

type desktopLifecycle struct {
	ctx context.Context
}

func (l *desktopLifecycle) startup(ctx context.Context) { l.ctx = ctx }

func runDesktop() (runErr error) {
	application, err := bootstrap.New(bootstrap.Options{Context: context.Background(), ApplicationDir: "playlist-forge"})
	if err != nil {
		return err
	}
	defer func() {
		if err := application.Close(); runErr == nil {
			runErr = err
		}
	}()
	lifecycle := &desktopLifecycle{}
	desktopAPI := desktop.New(context.Background(), application.Service, application.Keys, application.Validator, func(raw string) {
		if lifecycle.ctx != nil {
			wailsruntime.BrowserOpenURL(lifecycle.ctx, raw)
		}
	})
	assets, err := fs.Sub(desktopAssets, "internal/webui/dist")
	if err != nil {
		return fmt.Errorf("open desktop assets: %w", err)
	}
	return wails.Run(&options.App{
		Title:            "Playlist Forge",
		Width:            1280,
		Height:           800,
		MinWidth:         900,
		MinHeight:        640,
		BackgroundColour: &options.RGBA{R: 16, G: 17, B: 15, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        lifecycle.startup,
		Bind:             []interface{}{desktopAPI},
	})
}

func main() {
	if err := runDesktop(); err != nil {
		fmt.Fprintln(os.Stderr, "playlist-forge:", err)
		os.Exit(1)
	}
}
