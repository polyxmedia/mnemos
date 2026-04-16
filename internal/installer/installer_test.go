package installer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/installer"
)

func TestInstallCreatesFile(t *testing.T) {
	dir := t.TempDir()
	target := installer.Target{
		Name: "Claude Code", Path: filepath.Join(dir, ".claude.json"), Key: "mnemos",
	}
	changed, err := installer.Install(target, installer.ServerEntry{
		Command: "mnemos", Args: []string{"serve"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true on first install")
	}
	data, _ := os.ReadFile(target.Path)
	var cfg map[string]any
	_ = json.Unmarshal(data, &cfg)
	servers := cfg["mcpServers"].(map[string]any)
	entry := servers["mnemos"].(map[string]any)
	if entry["command"] != "mnemos" {
		t.Errorf("wrong command: %v", entry)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	target := installer.Target{
		Name: "Claude Code", Path: filepath.Join(dir, ".claude.json"), Key: "mnemos",
	}
	entry := installer.ServerEntry{Command: "mnemos", Args: []string{"serve"}}
	if _, err := installer.Install(target, entry); err != nil {
		t.Fatal(err)
	}
	changed, err := installer.Install(target, entry)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("expected changed=false on second install with identical entry")
	}
}

func TestInstallPreservesOtherServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	initial := `{
	"theme": "dark",
	"mcpServers": {
		"existing": {"command": "other-tool"}
	}
}`
	_ = os.WriteFile(path, []byte(initial), 0o644)

	target := installer.Target{Name: "Claude Code", Path: path, Key: "mnemos"}
	if _, err := installer.Install(target, installer.ServerEntry{Command: "mnemos"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var cfg map[string]any
	_ = json.Unmarshal(data, &cfg)
	if cfg["theme"] != "dark" {
		t.Error("unrelated root keys must be preserved")
	}
	servers := cfg["mcpServers"].(map[string]any)
	if _, ok := servers["existing"]; !ok {
		t.Error("unrelated mcp servers must be preserved")
	}
	if _, ok := servers["mnemos"]; !ok {
		t.Error("mnemos entry should be added")
	}
}

func TestUninstallRemovesEntry(t *testing.T) {
	dir := t.TempDir()
	target := installer.Target{
		Name: "Claude Code", Path: filepath.Join(dir, ".claude.json"), Key: "mnemos",
	}
	_, _ = installer.Install(target, installer.ServerEntry{Command: "mnemos"})
	if !installer.IsInstalled(target) {
		t.Fatal("expected installed")
	}
	changed, err := installer.Uninstall(target)
	if err != nil || !changed {
		t.Fatalf("uninstall: changed=%v err=%v", changed, err)
	}
	if installer.IsInstalled(target) {
		t.Error("expected not installed after uninstall")
	}
}
