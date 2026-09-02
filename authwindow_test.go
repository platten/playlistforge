package main

import (
	"strings"
	"testing"

	"playlistforge/internal/musicsource"
)

func TestHandleRawMessageFansCaptureToWaiter(t *testing.T) {
	ch := make(chan string, 1)
	authCaptures.Lock()
	authCaptures.pending = ch
	authCaptures.Unlock()
	t.Cleanup(func() {
		authCaptures.Lock()
		authCaptures.pending = nil
		authCaptures.Unlock()
	})

	// A non-matching message is ignored.
	handleRawMessage(nil, "wails:something", nil)
	// So is a bare prefix with no value.
	handleRawMessage(nil, authCapturePrefix, nil)
	select {
	case v := <-ch:
		t.Fatalf("unexpected capture %q", v)
	default:
	}

	handleRawMessage(nil, authCapturePrefix+`{"id":1,"token":"abc"}`, nil)
	select {
	case v := <-ch:
		if v != `{"id":1,"token":"abc"}` {
			t.Fatalf("capture = %q", v)
		}
	default:
		t.Fatal("capture was not delivered")
	}
}

func TestHandleRawMessageWithNoWaiterIsSafe(t *testing.T) {
	authCaptures.Lock()
	authCaptures.pending = nil
	authCaptures.Unlock()
	handleRawMessage(nil, authCapturePrefix+"value", nil) // must not panic
}

func TestCaptureProbeEmbedsRequest(t *testing.T) {
	probe := captureProbe(musicsource.AuthRequest{
		RedirectPrefix: "https://tidal.com/android/login/auth",
		ExtractJS:      `window.localStorage.getItem("localuser")`,
	})
	for _, want := range []string{
		authCapturePrefix,
		"https://tidal.com/android/login/auth",
		`window.localStorage.getItem(\"localuser\")`, // %q-escaped into a JS string literal
		"setInterval",
		"messageHandlers.external",
		"__pfAuthProbe",
	} {
		if !strings.Contains(probe, want) {
			t.Fatalf("probe missing %q\n%s", want, probe)
		}
	}
}
