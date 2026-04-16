package installer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/installer"
)

func TestDetectTargetsReturnsSomething(t *testing.T) {
	// DetectTargets walks real $HOME. Even in clean CI this should either
	// return an empty slice or slice of valid candidates; it must never
	// panic and the slice should contain no zero-valued entries.
	targets := installer.DetectTargets()
	for _, tg := range targets {
		if tg.Name == "" || tg.Path == "" || tg.Key == "" {
			t.Errorf("malformed target: %+v", tg)
		}
	}
}

func TestInstallRejectsBadExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.json")
	_ = os.WriteFile(path, []byte("{ not json"), 0o644)

	target := installer.Target{Name: "x", Path: path, Key: "mnemos"}
	_, err := installer.Install(target, installer.ServerEntry{Command: "mnemos"})
	if err == nil {
		t.Error("expected error on malformed existing config")
	}
}

func TestInstallWritesAtomically(t *testing.T) {
	dir := t.TempDir()
	target := installer.Target{Name: "x", Path: filepath.Join(dir, "x.json"), Key: "mnemos"}
	if _, err := installer.Install(target, installer.ServerEntry{Command: "mnemos"}); err != nil {
		t.Fatal(err)
	}
	// Temp file must not leak on success.
	if _, err := os.Stat(target.Path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file leaked: %v", err)
	}
}

func TestInstallWithEnvAndArgs(t *testing.T) {
	dir := t.TempDir()
	target := installer.Target{Name: "x", Path: filepath.Join(dir, "x.json"), Key: "mnemos"}
	if _, err := installer.Install(target, installer.ServerEntry{
		Command: "mnemos", Args: []string{"serve", "--http", ":8080"},
		Env: map[string]string{"MNEMOS_API_KEY": "secret"},
	}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target.Path)
	var cfg map[string]any
	_ = json.Unmarshal(data, &cfg)
	entry := cfg["mcpServers"].(map[string]any)["mnemos"].(map[string]any)
	if entry["env"] == nil {
		t.Errorf("env not persisted: %+v", entry)
	}
	if args, ok := entry["args"].([]any); !ok || len(args) != 3 {
		t.Errorf("args not persisted correctly: %v", entry["args"])
	}
}

func TestUninstallNoEntryIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	_ = os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o644)
	target := installer.Target{Name: "x", Path: path, Key: "mnemos"}
	changed, err := installer.Uninstall(target)
	if err != nil || changed {
		t.Errorf("uninstall on empty should be no-op: changed=%v err=%v", changed, err)
	}
}

func TestUninstallMissingFileIsNoOp(t *testing.T) {
	target := installer.Target{Name: "x", Path: "/tmp/does-not-exist.json", Key: "mnemos"}
	changed, err := installer.Uninstall(target)
	if err != nil || changed {
		t.Errorf("missing file uninstall should be no-op: changed=%v err=%v", changed, err)
	}
}
