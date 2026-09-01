package main

import (
	"path/filepath"
	"testing"
)

func TestEnvironmentHelpers(t *testing.T) {
	t.Setenv("PLAYLIST_FORGE_PORT", "9191")
	if got := envPort(); got != 9191 {
		t.Fatalf("port = %d", got)
	}
	t.Setenv("PLAYLIST_FORGE_PORT", "invalid")
	if got := envPort(); got != defaultPort {
		t.Fatalf("fallback port = %d", got)
	}
	t.Setenv("TEST_STRING", "value")
	if got := envString("TEST_STRING", "fallback"); got != "value" {
		t.Fatalf("string = %q", got)
	}
	t.Setenv("TEST_STRING", "")
	if got := envString("TEST_STRING", "fallback"); got != "fallback" {
		t.Fatalf("fallback string = %q", got)
	}
	t.Setenv("TEST_BOOL", "false")
	if envBool("TEST_BOOL", true) {
		t.Fatal("boolean environment was not parsed")
	}
	t.Setenv("TEST_BOOL", "invalid")
	if !envBool("TEST_BOOL", true) {
		t.Fatal("invalid boolean did not use fallback")
	}
}

func TestConfigDirOverride(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveConfigDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("config dir = %q, want %q", got, want)
	}
	if standard, err := resolveConfigDir(""); err != nil || filepath.Base(standard) != "playlist-forge" {
		t.Fatalf("standard config dir = %q, %v", standard, err)
	}
}
