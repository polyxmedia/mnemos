package installer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/polyxmedia/mnemos/internal/installer"
)

func codexTarget(path string) installer.Target {
	return installer.Target{
		Name:   "OpenAI Codex CLI",
		Path:   path,
		Group:  "mcp_servers",
		Key:    "mnemos",
		Format: installer.FormatTOML,
	}
}

func TestInstallTOMLCreatesFile(t *testing.T) {
	dir := t.TempDir()
	target := codexTarget(filepath.Join(dir, "config.toml"))
	changed, err := installer.Install(target, installer.ServerEntry{
		Command: "/usr/local/bin/mnemos", Args: []string{"serve"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on first install")
	}

	data, _ := os.ReadFile(target.Path)
	if !strings.Contains(string(data), "[mcp_servers.mnemos]") {
		t.Fatalf("missing mcp_servers.mnemos table:\n%s", data)
	}

	// Round-trip decode to verify structure.
	var cfg map[string]any
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		t.Fatalf("round-trip decode: %v", err)
	}
	servers, ok := cfg["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers not a table: %T", cfg["mcp_servers"])
	}
	entry, ok := servers["mnemos"].(map[string]any)
	if !ok {
		t.Fatalf("mnemos entry not a table: %T", servers["mnemos"])
	}
	if entry["command"] != "/usr/local/bin/mnemos" {
		t.Errorf("wrong command: %v", entry["command"])
	}
}

func TestInstallTOMLIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	target := codexTarget(filepath.Join(dir, "config.toml"))
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

func TestInstallTOMLPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := `model = "gpt-5"

[mcp_servers.other-tool]
command = "other"
args = ["run"]
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	target := codexTarget(path)
	if _, err := installer.Install(target, installer.ServerEntry{
		Command: "mnemos", Args: []string{"serve"},
	}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	var cfg map[string]any
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg["model"] != "gpt-5" {
		t.Errorf("top-level key lost: %v", cfg["model"])
	}
	servers := cfg["mcp_servers"].(map[string]any)
	if _, ok := servers["other-tool"]; !ok {
		t.Error("unrelated mcp_servers entry must be preserved")
	}
	if _, ok := servers["mnemos"]; !ok {
		t.Error("mnemos entry should be added")
	}
}

func TestUninstallTOMLRemovesEntry(t *testing.T) {
	dir := t.TempDir()
	target := codexTarget(filepath.Join(dir, "config.toml"))
	if _, err := installer.Install(target, installer.ServerEntry{
		Command: "mnemos", Args: []string{"serve"},
	}); err != nil {
		t.Fatal(err)
	}
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

func TestInstallTOMLRejectsBadExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.toml")
	if err := os.WriteFile(path, []byte("this = is = not = toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := codexTarget(path)
	if _, err := installer.Install(target, installer.ServerEntry{Command: "mnemos"}); err == nil {
		t.Error("expected error on malformed existing config")
	}
}

func TestInstallTOMLUpdatesExistingEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := `[mcp_servers.mnemos]
command = "/old/path/mnemos"
args = ["serve"]
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	target := codexTarget(path)
	changed, err := installer.Install(target, installer.ServerEntry{
		Command: "/new/path/mnemos", Args: []string{"serve"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true when command differs")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "/new/path/mnemos") {
		t.Errorf("new command not written:\n%s", data)
	}
	if strings.Contains(string(data), "/old/path/mnemos") {
		t.Errorf("old command not replaced:\n%s", data)
	}
}
