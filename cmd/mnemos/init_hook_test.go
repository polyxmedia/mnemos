package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/installer"
)

// setupClaudeCodeHome builds an isolated $HOME with an empty .claude.json and
// returns it. The CLAUDE_CONFIG_DIR override lets the installer target the
// isolated tree without touching the developer's real configuration.
func setupClaudeCodeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	// Seed .claude.json so DetectTargets picks up Claude Code (user).
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("seed .claude.json: %v", err)
	}
	// Seed the .mnemos dir so loadDeps-dependent paths do not surprise
	// the init command (init itself does not hit the DB, but defensive).
	_ = os.MkdirAll(filepath.Join(home, ".mnemos"), 0o755)
	return home
}

func TestRunInitWiresHookForClaudeCode(t *testing.T) {
	home := setupClaudeCodeHome(t)

	out := captureStdout(t, func() {
		if err := runInit(context.Background(), nil); err != nil {
			t.Fatalf("init: %v", err)
		}
	})
	if !strings.Contains(out, "SessionStart hook wired") {
		t.Errorf("expected hook wired line, got: %s", out)
	}

	// Verify settings.json now has the SessionStart entry.
	settingsPath := filepath.Join(home, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	hooks, ok := cfg["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing")
	}
	sessionStart, ok := hooks["SessionStart"].([]any)
	if !ok || len(sessionStart) != 1 {
		t.Fatalf("expected exactly one SessionStart group, got %v", sessionStart)
	}
	group := sessionStart[0].(map[string]any)
	if group["matcher"] != "startup" {
		t.Errorf("matcher should be 'startup', got %v", group["matcher"])
	}
	inner := group["hooks"].([]any)
	cmd := inner[0].(map[string]any)
	if !strings.HasSuffix(cmd["command"].(string), "prewarm") {
		t.Errorf("hook command should end with 'prewarm', got %v", cmd["command"])
	}
}

func TestRunInitWiresUserPromptSubmitHookForClaudeCode(t *testing.T) {
	home := setupClaudeCodeHome(t)

	out := captureStdout(t, func() {
		if err := runInit(context.Background(), nil); err != nil {
			t.Fatalf("init: %v", err)
		}
	})
	if !strings.Contains(out, "UserPromptSubmit hook wired") {
		t.Errorf("expected UserPromptSubmit hook wired line, got: %s", out)
	}

	data, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	hooks := cfg["hooks"].(map[string]any)
	groups, ok := hooks["UserPromptSubmit"].([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("expected exactly one UserPromptSubmit group, got %v", groups)
	}
	inner := groups[0].(map[string]any)["hooks"].([]any)
	cmd := inner[0].(map[string]any)["command"].(string)
	if !strings.HasSuffix(cmd, "hook user-prompt") {
		t.Errorf("UserPromptSubmit command should end with 'hook user-prompt', got %q", cmd)
	}
}

func TestRunInitWiresPostToolUseHookForClaudeCode(t *testing.T) {
	home := setupClaudeCodeHome(t)

	out := captureStdout(t, func() {
		if err := runInit(context.Background(), nil); err != nil {
			t.Fatalf("init: %v", err)
		}
	})
	if !strings.Contains(out, "PostToolUse hook wired") {
		t.Errorf("expected PostToolUse hook wired line, got: %s", out)
	}

	data, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(data, &cfg)
	hooks := cfg["hooks"].(map[string]any)
	groups, ok := hooks["PostToolUse"].([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("expected one PostToolUse group, got %v", groups)
	}
	group := groups[0].(map[string]any)
	if group["matcher"] != "Edit|Write|MultiEdit|NotebookEdit" {
		t.Errorf("unexpected PostToolUse matcher: %v", group["matcher"])
	}
	inner := group["hooks"].([]any)
	cmd := inner[0].(map[string]any)["command"].(string)
	if !strings.HasSuffix(cmd, "hook post-tool") {
		t.Errorf("PostToolUse command should end with 'hook post-tool', got %q", cmd)
	}
}

func TestRunInitWiresSessionEndHookForClaudeCode(t *testing.T) {
	home := setupClaudeCodeHome(t)

	out := captureStdout(t, func() {
		if err := runInit(context.Background(), nil); err != nil {
			t.Fatalf("init: %v", err)
		}
	})
	if !strings.Contains(out, "SessionEnd hook wired") {
		t.Errorf("expected SessionEnd hook wired line, got: %s", out)
	}

	data, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	var cfg map[string]any
	_ = json.Unmarshal(data, &cfg)
	hooks := cfg["hooks"].(map[string]any)
	groups, ok := hooks["SessionEnd"].([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("expected one SessionEnd group, got %v", groups)
	}
	inner := groups[0].(map[string]any)["hooks"].([]any)
	cmd := inner[0].(map[string]any)["command"].(string)
	if !strings.HasSuffix(cmd, "hook session-end") {
		t.Errorf("SessionEnd command should end with 'hook session-end', got %q", cmd)
	}
}

func TestRunInitWiresPreToolGuardrailForClaudeCode(t *testing.T) {
	home := setupClaudeCodeHome(t)

	out := captureStdout(t, func() {
		if err := runInit(context.Background(), nil); err != nil {
			t.Fatalf("init: %v", err)
		}
	})
	if !strings.Contains(out, "PreToolUse guardrail wired") {
		t.Errorf("expected PreToolUse guardrail wired line, got: %s", out)
	}

	data, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	var cfg map[string]any
	_ = json.Unmarshal(data, &cfg)
	hooks := cfg["hooks"].(map[string]any)
	groups, ok := hooks["PreToolUse"].([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("expected one PreToolUse group, got %v", groups)
	}
	group := groups[0].(map[string]any)
	want := "mcp__mnemos__mnemos_save|mcp__mnemos__mnemos_correct|mcp__mnemos__mnemos_convention"
	if group["matcher"] != want {
		t.Errorf("unexpected PreToolUse matcher: %v", group["matcher"])
	}
	inner := group["hooks"].([]any)
	cmd := inner[0].(map[string]any)["command"].(string)
	if !strings.HasSuffix(cmd, "hook pre-tool") {
		t.Errorf("PreToolUse command should end with 'hook pre-tool', got %q", cmd)
	}
}

func TestRunInitHookIsIdempotent(t *testing.T) {
	setupClaudeCodeHome(t)

	// First run wires.
	_ = captureStdout(t, func() {
		_ = runInit(context.Background(), nil)
	})
	// Second run must report already-up-to-date for the hook too.
	out := captureStdout(t, func() {
		_ = runInit(context.Background(), nil)
	})
	if !strings.Contains(out, "SessionStart hook already up to date") {
		t.Errorf("expected idempotent message, got: %s", out)
	}
}

func TestRunInitSkipsHookWhenNoClaudeCode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Point CLAUDE_CONFIG_DIR at a non-existent path so DetectTargets will
	// not match Claude Code (user). Its parent-dir heuristic otherwise
	// matches any extant $HOME, which would defeat the test.
	missing := filepath.Join(home, "no", "such", "dir")
	t.Setenv("CLAUDE_CONFIG_DIR", missing)
	// Seed a Cursor config dir so at least one target is detected (init
	// would otherwise short-circuit on "no agent clients detected").
	_ = os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)

	out := captureStdout(t, func() {
		_ = runInit(context.Background(), nil)
	})
	if strings.Contains(out, "SessionStart hook") {
		t.Errorf("hook line must be absent when Claude Code not detected: %s", out)
	}
	// settings.json at the configured (non-existent) location must not
	// have been force-created either.
	if _, err := os.Stat(filepath.Join(missing, "settings.json")); err == nil {
		t.Error("settings.json must not be created without Claude Code")
	}
}

func TestRunDoctorPassesAfterInit(t *testing.T) {
	setupClaudeCodeHome(t)
	_ = captureStdout(t, func() {
		_ = runInit(context.Background(), nil)
	})

	out := captureStdout(t, func() {
		if err := runDoctor(context.Background(), nil); err != nil {
			t.Fatalf("doctor: %v", err)
		}
	})
	if !strings.Contains(out, "SessionStart hook") {
		t.Errorf("doctor should report the hook: %s", out)
	}
	if !strings.Contains(out, "all checks passed") {
		t.Errorf("doctor should pass after init: %s", out)
	}
}

func TestRunDoctorFailsWithoutHook(t *testing.T) {
	home := setupClaudeCodeHome(t)
	// Wire MCP only (skip hook).
	selfPath, _ := os.Executable()
	_, _ = installer.Install(installer.Target{
		Name: "Claude Code (user)",
		Path: filepath.Join(home, ".claude.json"),
		Key:  "mnemos",
	}, installer.ServerEntry{Command: selfPath, Args: []string{"serve"}})

	err := runDoctor(context.Background(), nil)
	if err == nil {
		t.Error("doctor must fail when the SessionStart hook is missing")
	}
}
