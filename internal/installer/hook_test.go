package installer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/installer"
)

func TestInstallHookCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	entry := installer.HookEntry{
		Matcher: "startup", Command: "/usr/local/bin/mnemos prewarm", Timeout: 10,
	}

	changed, err := installer.InstallHook(path, entry)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !changed {
		t.Error("expected changed=true on first install")
	}

	cfg := readJSON(t, path)
	hooks := cfg["hooks"].(map[string]any)
	list := hooks["SessionStart"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 session_start group, got %d", len(list))
	}
	group := list[0].(map[string]any)
	if group["matcher"] != "startup" {
		t.Errorf("wrong matcher: %v", group["matcher"])
	}
	inner := group["hooks"].([]any)
	cmd := inner[0].(map[string]any)
	if cmd["command"] != entry.Command {
		t.Errorf("wrong command: %v", cmd["command"])
	}
	if cmd["type"] != "command" {
		t.Errorf("type must be 'command', got %v", cmd["type"])
	}
	// Timeout survives the round trip as a number. JSON decoders pick
	// float64 by default, which is fine — Claude Code parses it as int.
	if cmd["timeout"] != float64(10) {
		t.Errorf("timeout should be 10, got %v", cmd["timeout"])
	}
}

func TestInstallHookIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	entry := installer.HookEntry{
		Matcher: "startup", Command: "/usr/local/bin/mnemos prewarm",
	}
	if _, err := installer.InstallHook(path, entry); err != nil {
		t.Fatal(err)
	}
	changed, err := installer.InstallHook(path, entry)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("expected changed=false on second install")
	}
}

func TestInstallHookPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	initial := `{
		"theme": "dark",
		"env": {"FOO": "bar"},
		"hooks": {
			"UserPromptSubmit": [
				{"hooks": [{"type": "command", "command": "lint"}]}
			]
		}
	}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.InstallHook(path, installer.HookEntry{
		Matcher: "startup", Command: "mnemos prewarm",
	}); err != nil {
		t.Fatal(err)
	}

	cfg := readJSON(t, path)
	if cfg["theme"] != "dark" {
		t.Error("theme must be preserved")
	}
	if env, _ := cfg["env"].(map[string]any); env["FOO"] != "bar" {
		t.Error("env must be preserved")
	}
	hooks := cfg["hooks"].(map[string]any)
	if ups, ok := hooks["UserPromptSubmit"].([]any); !ok || len(ups) != 1 {
		t.Error("UserPromptSubmit hook must be preserved")
	}
	if ss, ok := hooks["SessionStart"].([]any); !ok || len(ss) != 1 {
		t.Error("SessionStart should have our new entry")
	}
}

func TestInstallHookAppendsWhenDifferentCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	initial := `{
		"hooks": {
			"SessionStart": [
				{"matcher": "startup", "hooks": [{"type": "command", "command": "other-tool"}]}
			]
		}
	}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := installer.InstallHook(path, installer.HookEntry{
		Matcher: "startup", Command: "mnemos prewarm",
	})
	if err != nil || !changed {
		t.Fatalf("install: changed=%v err=%v", changed, err)
	}
	cfg := readJSON(t, path)
	list := cfg["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(list) != 2 {
		t.Errorf("expected both entries (other + mnemos), got %d: %v", len(list), list)
	}
}

func TestInstallHookReplacesEntryWithSameCommandDifferentTimeout(t *testing.T) {
	// Rationale: if a user upgrades mnemos and the binary path stays stable
	// but we want to change the default timeout, the install should be a
	// no-op on command match — timeout drift is not worth rewriting the
	// file. Users can uninstall+reinstall if they really want a fresh entry.
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	entry := installer.HookEntry{
		Matcher: "startup", Command: "mnemos prewarm", Timeout: 10,
	}
	if _, err := installer.InstallHook(path, entry); err != nil {
		t.Fatal(err)
	}
	entry2 := entry
	entry2.Timeout = 30
	changed, err := installer.InstallHook(path, entry2)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("command match should be idempotent regardless of timeout drift")
	}
}

func TestUninstallHookRemovesEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	entry := installer.HookEntry{
		Matcher: "startup", Command: "mnemos prewarm",
	}
	if _, err := installer.InstallHook(path, entry); err != nil {
		t.Fatal(err)
	}
	if !installer.IsHookInstalled(path, entry) {
		t.Fatal("expected installed")
	}
	changed, err := installer.UninstallHook(path, entry)
	if err != nil || !changed {
		t.Fatalf("uninstall: changed=%v err=%v", changed, err)
	}
	if installer.IsHookInstalled(path, entry) {
		t.Error("expected not installed after uninstall")
	}

	// The SessionStart key should be gone entirely since it only held our
	// entry. Leaving an empty array back is untidy and could confuse
	// Claude Code's hook loader on some versions.
	cfg := readJSON(t, path)
	if hooks, ok := cfg["hooks"].(map[string]any); ok {
		if _, exists := hooks["SessionStart"]; exists {
			t.Error("SessionStart should be removed when empty")
		}
	}
}

func TestUninstallHookPreservesOtherGroups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	ours := installer.HookEntry{Matcher: "startup", Command: "mnemos prewarm"}
	other := installer.HookEntry{Matcher: "startup", Command: "other-tool"}
	if _, err := installer.InstallHook(path, other); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.InstallHook(path, ours); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.UninstallHook(path, ours); err != nil {
		t.Fatal(err)
	}
	if installer.IsHookInstalled(path, ours) {
		t.Error("ours must be gone")
	}
	if !installer.IsHookInstalled(path, other) {
		t.Error("other must remain")
	}
}

func TestUninstallHookMissingFile(t *testing.T) {
	dir := t.TempDir()
	changed, err := installer.UninstallHook(filepath.Join(dir, "no-such.json"),
		installer.HookEntry{Matcher: "startup", Command: "mnemos prewarm"})
	if err != nil {
		t.Errorf("missing file should be silent, got %v", err)
	}
	if changed {
		t.Error("missing file cannot have changed")
	}
}

func TestClaudeSettingsPathHonoursEnv(t *testing.T) {
	home := t.TempDir()
	override := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	t.Setenv("CLAUDE_CONFIG_DIR", override)
	got := installer.ClaudeSettingsPath()
	want := filepath.Join(override, "settings.json")
	if got != want {
		t.Errorf("with env: got %q, want %q", got, want)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", "")
	got = installer.ClaudeSettingsPath()
	want = filepath.Join(home, ".claude", "settings.json")
	if got != want {
		t.Errorf("without env: got %q, want %q", got, want)
	}
}

func TestInstallHookWithoutMatcher(t *testing.T) {
	// Claude Code treats an absent matcher as a wildcard. We emit no
	// "matcher" key when entry.Matcher is empty so settings.json stays tidy.
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if _, err := installer.InstallHook(path, installer.HookEntry{
		Command: "mnemos prewarm",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := readJSON(t, path)
	group := cfg["hooks"].(map[string]any)["SessionStart"].([]any)[0].(map[string]any)
	if _, ok := group["matcher"]; ok {
		t.Error("matcher key should be omitted when empty")
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}
