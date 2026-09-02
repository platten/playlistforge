package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestOpenInBrowserErrsWhenNoOpenerExists(t *testing.T) {
	t.Setenv("PATH", "")
	if err := openInBrowser("https://example.com"); err == nil {
		t.Fatal("expected an error when PATH holds no browser opener")
	}
}

func TestOpenInBrowserUsesAnOpenerOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub is POSIX-only")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "opened")
	// Masquerade as xdg-open, the first candidate tried on linux.
	stub := filepath.Join(dir, "xdg-open")
	script := "#!/bin/sh\necho \"$1\" > " + marker + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir)

	if err := openInBrowser("https://example.com/approve"); err != nil {
		t.Fatalf("openInBrowser: %v", err)
	}
	// The opener is started, not waited on; give it a moment.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil {
			if got := string(data); got != "https://example.com/approve\n" {
				t.Fatalf("stub saw %q", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("browser opener was not invoked")
}
