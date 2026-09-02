package main

import (
	"errors"
	"os/exec"
	"runtime"
)

// openInBrowser launches target in the user's default browser, trying several
// openers in turn. Wails' own opener is xdg-open only, which is absent on WSL
// and on minimal Linux installs; the extra candidates cover WSL (reaching the
// Windows browser through the interop) and alternative desktop helpers.
func openInBrowser(target string) error {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"open", target}}
	case "windows":
		candidates = [][]string{{"rundll32", "url.dll,FileProtocolHandler", target}}
	default: // linux, including WSL
		candidates = [][]string{
			{"xdg-open", target},
			{"wslview", target},
			{"cmd.exe", "/c", "start", "", target},
			{"powershell.exe", "-NoProfile", "-Command", "Start-Process", target},
			{"sensible-browser", target},
			{"x-www-browser", target},
			{"gio", "open", target},
		}
	}

	lastErr := errors.New("no browser opener is available")
	for _, argv := range candidates {
		if _, err := exec.LookPath(argv[0]); err != nil {
			lastErr = err
			continue
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		if err := cmd.Start(); err != nil {
			lastErr = err
			continue
		}
		go func() { _ = cmd.Wait() }() // reap without blocking
		return nil
	}
	return lastErr
}
