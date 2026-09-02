package main

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"playlistforge/internal/musicsource"
)

// authPollInterval is how often the sign-in window is probed for a captured
// redirect URL or token.
const authPollInterval = 600 * time.Millisecond

// authCapturePrefix marks raw messages the sign-in window posts back to Go.
const authCapturePrefix = "pf-auth:"

// authCaptures fans a captured value from the app-wide RawMessageHandler to the
// runAuth call that is currently waiting. Only one sign-in runs at a time.
var authCaptures = struct {
	sync.Mutex
	pending chan string
}{}

// handleRawMessage is registered as application.Options.RawMessageHandler. It
// forwards a sign-in capture to a waiting runAuth and ignores everything else.
func handleRawMessage(_ application.Window, message string, _ *application.OriginInfo) {
	if len(message) <= len(authCapturePrefix) || message[:len(authCapturePrefix)] != authCapturePrefix {
		return
	}
	authCaptures.Lock()
	ch := authCaptures.pending
	authCaptures.Unlock()
	if ch != nil {
		select {
		case ch <- message[len(authCapturePrefix):]:
		default:
		}
	}
}

// runAuth opens the streaming sign-in window described by req and blocks until
// it captures the redirect URL (req.RedirectPrefix) or a token pulled from page
// state (req.ExtractJS), the listener closes the window, or three minutes pass.
//
// NOTE: this path exercises a real service's web login inside an embedded
// webview and can only be validated against TIDAL/Qobuz with real credentials.
// It compiles and is wired, but the TIDAL and Qobuz adapters (and this flow)
// still need hands-on iteration.
func runAuth(req musicsource.AuthRequest) (string, error) {
	if req.URL == "" {
		return "", errors.New("provider gave no sign-in URL")
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
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Sign in",
		Width:  width,
		Height: height,
		URL:    req.URL,
	})

	// After every navigation, probe the page and post any capture back to Go.
	probe := fmt.Sprintf(`(function(){
try{
  var href = String(location.href);
  if (%q && href.indexOf(%q) === 0) { post(href); return; }
  var extract = %q;
  if (extract) {
    var v = (function(){ try { return eval(extract); } catch(e) { return ""; } })();
    if (v) post(String(v));
  }
  function post(s){
    var m = %q + s;
    if (window.chrome && window.chrome.webview) window.chrome.webview.postMessage(m);
    else if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.external) window.webkit.messageHandlers.external.postMessage(m);
  }
}catch(e){}
})();`, req.RedirectPrefix, req.RedirectPrefix, req.ExtractJS, authCapturePrefix)

	closed := make(chan struct{}, 1)
	window.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		select {
		case closed <- struct{}{}:
		default:
		}
	})

	// Poll the page rather than hook a platform-specific navigation event: the
	// probe posts back through handleRawMessage the moment it sees the capture.
	ticker := time.NewTicker(authPollInterval)
	defer ticker.Stop()
	deadline := time.After(3 * time.Minute)
	for {
		select {
		case value := <-result:
			window.Close()
			return value, nil
		case <-closed:
			return "", errors.New("sign-in window was closed")
		case <-deadline:
			window.Close()
			return "", errors.New("sign-in timed out")
		case <-ticker.C:
			window.ExecJS(probe)
		}
	}
}
