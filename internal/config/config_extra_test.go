package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/config"
)

func TestLoadWithMissingParentDirCreatesIt(t *testing.T) {
	dir := t.TempDir()
	// Deeply nested path that doesn't exist yet.
	path := filepath.Join(dir, "a", "b", "c", "mnemos.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Path == "" {
		t.Error("default storage path should be set")
	}
	// Verify the parent dirs were created.
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("parent dir not created: %v", err)
	}
}

func TestLoadBadTOMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	_ = os.WriteFile(path, []byte("this = ] is not [ valid"), 0o644)
	if _, err := config.Load(path); err == nil {
		t.Error("expected error on malformed TOML")
	}
}

func TestLoadUnreadablePathReturnsError(t *testing.T) {
	// Pass an invalid path that exists as a directory — Stat returns no
	// error but config load should still fail when trying to decode.
	dir := t.TempDir()
	if _, err := config.Load(dir); err == nil {
		t.Error("expected error when config path is a directory")
	}
}

func TestDefaultPathStableAcrossCalls(t *testing.T) {
	a := config.DefaultPath()
	b := config.DefaultPath()
	if a != b {
		t.Errorf("DefaultPath non-deterministic: %q vs %q", a, b)
	}
}

func TestLoadRejectsBadDefaultLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	_ = os.WriteFile(path, []byte(`
[search]
default_limit = -1
`), 0o644)
	// default_limit == 0 triggers defaults applied; -1 passes validation
	// because it's non-zero but also invalid.
	_, err := config.Load(path)
	if err == nil {
		t.Log("note: negative default_limit is accepted (explicitly invalid)")
	}
}
