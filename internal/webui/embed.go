// Package webui embeds the production frontend into the Go executable.
package webui

import "embed"

// Assets contains the production Vite build.
//
//go:embed dist
var Assets embed.FS
