package main

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"playlistforge/internal/musicsource"
)

// authCapturePrefix marks raw messages the sign-in window posts back to Go.
const authCapturePrefix = "pf-auth:"

// authSignInDeadline bounds how long the sign-in window stays open waiting for a
// capture (real logins can involve email/2FA steps).
const authSignInDeadline = 5 * time.Minute

// authCaptures fans a captured value from the app-wide RawMessageHandler to the
// runAuth call that is currently waiting. Only one sign-in runs at a time.
var authCaptures = struct {
	sync.Mutex
	pending chan string
}{}

// handleRawMessage is registered as application.Options.RawMessageHandler. It
// forwards a sign-in capture to a waiting runAuth and ignores everything else.
func handleRawMessage(_ application.Window, message string, _ *application.OriginInfo) {
	value, ok := strings.CutPrefix(message, authCapturePrefix)
	if !ok || value == "" {
		return
	}
	authCaptures.Lock()
	ch := authCaptures.pending
	authCaptures.Unlock()
	if ch != nil {
		select {
		case ch <- value:
			slog.Info("streaming sign-in: capture received", "bytes", len(value))
		default:
		}
	}
}

// runAuth opens the streaming sign-in window described by req and blocks until
// it captures the redirect URL (req.RedirectPrefix) or a value pulled from page
// state (req.ExtractJS), the user closes the window, or the deadline passes.
//
// The capture probe is injected as the window's page script (options.JS), which
// Wails re-runs on every load: WebviewWindow.ExecJS only runs once the Wails
// runtime has loaded, and these pages are third-party logins that never load it.
// The probe installs its own interval so it keeps checking across the SPA route
// changes a login flow makes without a full navigation.
func runAuth(req musicsource.AuthRequest) (string, error) {
	if req.URL == "" {
		return "", errors.New("provider gave no sign-in URL")
	}

	// Device-flow providers approve in the user's real browser; the provider's
	// Complete polls for the result, so there is nothing to capture here. A
	// browser that can't be launched is not fatal: the poll still runs and the
	// URL is logged for the user to open by hand.
	if req.OpenInBrowser {
		slog.Info("streaming sign-in: opening in the system browser", "url", req.URL)
		if err := openInBrowser(req.URL); err != nil {
			slog.Warn("streaming sign-in: could not launch a browser — open this URL manually to approve", "url", req.URL, "error", err)
		}
		return "", nil
	}

	result := make(chan string, 1)
	authCaptures.Lock()
	if authCaptures.pending != nil {
		authCaptures.Unlock()
		return "", errors.New("a sign-in is already in progress")
	}
	authCaptures.pending = result
	authCaptures.Unlock()
	defer func() {
		authCaptures.Lock()
		authCaptures.pending = nil
		authCaptures.Unlock()
	}()

	app := application.Get()
	if app == nil {
		return "", errors.New("application is not running")
	}

	width, height := req.Width, req.Height
	if width <= 0 {
		width = 520
	}
	if height <= 0 {
		height = 720
	}

	slog.Info("streaming sign-in: opening window", "url", req.URL, "redirectPrefix", req.RedirectPrefix, "extract", req.ExtractJS != "")
	opts := application.WebviewWindowOptions{
		Title:  "Sign in",
		Width:  width,
		Height: height,
		URL:    req.URL,
		JS:     captureProbe(req),
	}
	if runtime.GOOS == "windows" {
		// WebView2 injects options.JS only when options.HTML is also set (it
		// becomes an AddScriptToExecuteOnDocumentCreated init script that then
		// runs on every document). Bootstrap through a page that immediately
		// redirects, so the capture probe is registered for the real sign-in
		// page too. Linux/macOS inject options.JS on every navigation already.
		opts.HTML = fmt.Sprintf(
			`<!doctype html><meta charset="utf-8"><title>Sign in</title>`+
				`<body style="background:#0b0c0a"></body>`+
				`<script>location.replace(%q)</script>`, req.URL)
	}
	window := app.Window.NewWithOptions(opts)

	closed := make(chan struct{}, 1)
	window.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		select {
		case closed <- struct{}{}:
		default:
		}
	})

	select {
	case value := <-result:
		window.Close()
		return value, nil
	case <-closed:
		slog.Info("streaming sign-in: window closed before a capture")
		return "", errors.New("sign-in window was closed")
	case <-time.After(authSignInDeadline):
		window.Close()
		return "", errors.New("sign-in timed out")
	}
}

// captureProbe builds the page script that watches for the capture and posts it
// back through the "external" script-message bridge. It guards against being
// installed twice on the same document and polls on an interval so an SPA route
// change (no page load) is still noticed.
func captureProbe(req musicsource.AuthRequest) string {
	return fmt.Sprintf(`(function(){
  if (window.__pfAuthProbe) { return; }
  window.__pfAuthProbe = true;
  var PREFIX = %q, REDIRECT = %q, EXTRACT = %q;
  function post(s){
    try {
      var m = PREFIX + s;
      if (window.chrome && window.chrome.webview && window.chrome.webview.postMessage) { window.chrome.webview.postMessage(m); return; }
      if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.external) { window.webkit.messageHandlers.external.postMessage(m); return; }
    } catch (e) {}
  }
  function check(){
    try {
      if (REDIRECT && String(location.href).indexOf(REDIRECT) === 0) { post(String(location.href)); return; }
      if (EXTRACT) {
        var v = null;
        try { v = eval(EXTRACT); } catch (e) {}
        if (v) { post(String(v)); }
      }
    } catch (e) {}
  }
  check();
  window.setInterval(check, 500);
})();`, authCapturePrefix, req.RedirectPrefix, req.ExtractJS)
}
