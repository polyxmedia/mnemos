package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// HookEntry describes a Claude Code SessionStart hook to wire into
// settings.json. Multiple matchers (startup, resume, compact) can share the
// same command — we install one entry per matcher.
type HookEntry struct {
	Matcher string // "startup" | "resume" | "clear" | "compact" | "*"
	Command string // full command line, e.g. "/usr/local/bin/mnemos prewarm"
	Timeout int    // seconds; zero means omit the field
}

// ClaudeSettingsPath returns the path to the Claude Code user-scope
// settings.json. Honours CLAUDE_CONFIG_DIR for parity with how .claude.json
// is located; otherwise falls back to ~/.claude/settings.json.
func ClaudeSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(claudeSettingsDir(home), "settings.json")
}

func claudeSettingsDir(home string) string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(home, ".claude")
}

// InstallHook adds entry to settings.json under hooks.SessionStart. It is
// idempotent: if an entry with the same matcher and command already exists,
// it returns changed=false. Other hooks (whether for other events or other
// commands under SessionStart) are preserved untouched.
func InstallHook(path string, entry HookEntry) (bool, error) {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return false, err
	}
	cfg, err := readSettings(path)
	if err != nil {
		return false, err
	}

	hooks := asStringMap(cfg["hooks"])
	sessionStart := asAnyList(hooks["SessionStart"])
	desired := hookGroupFor(entry)

	if groupIndex(sessionStart, entry) >= 0 {
		return false, nil
	}
	sessionStart = append(sessionStart, desired)
	hooks["SessionStart"] = sessionStart
	cfg["hooks"] = hooks

	data, err := encodeJSON(cfg)
	if err != nil {
		return false, err
	}
	return true, writeAtomic(path, data)
}

// UninstallHook removes any SessionStart hook group whose matcher and command
// match entry. Returns changed=true if the file was rewritten.
func UninstallHook(path string, entry HookEntry) (bool, error) {
	cfg, err := readSettings(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	hooks := asStringMap(cfg["hooks"])
	sessionStart := asAnyList(hooks["SessionStart"])

	idx := groupIndex(sessionStart, entry)
	if idx < 0 {
		return false, nil
	}
	sessionStart = append(sessionStart[:idx], sessionStart[idx+1:]...)
	if len(sessionStart) == 0 {
		delete(hooks, "SessionStart")
	} else {
		hooks["SessionStart"] = sessionStart
	}
	if len(hooks) == 0 {
		delete(cfg, "hooks")
	} else {
		cfg["hooks"] = hooks
	}

	data, err := encodeJSON(cfg)
	if err != nil {
		return false, err
	}
	return true, writeAtomic(path, data)
}

// IsHookInstalled reports whether settings.json has a SessionStart group
// matching entry.
func IsHookInstalled(path string, entry HookEntry) bool {
	cfg, err := readSettings(path)
	if err != nil {
		return false
	}
	hooks := asStringMap(cfg["hooks"])
	return groupIndex(asAnyList(hooks["SessionStart"]), entry) >= 0
}

func readSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func encodeJSON(cfg map[string]any) ([]byte, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal settings: %w", err)
	}
	return append(data, '\n'), nil
}

func asAnyList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// hookGroupFor builds the JSON shape a single matcher-group takes in
// settings.json: { "matcher": "...", "hooks": [ {"type": "command", ...} ] }.
func hookGroupFor(entry HookEntry) map[string]any {
	cmd := map[string]any{
		"type":    "command",
		"command": entry.Command,
	}
	if entry.Timeout > 0 {
		cmd["timeout"] = entry.Timeout
	}
	group := map[string]any{"hooks": []any{cmd}}
	if entry.Matcher != "" {
		group["matcher"] = entry.Matcher
	}
	return group
}

// groupIndex returns the index of the first hook group whose matcher and
// embedded command match entry, or -1 if none. We dedupe on (matcher, command)
// so a user reinstalling with a new binary path replaces the old entry.
func groupIndex(list []any, entry HookEntry) int {
	for i, raw := range list {
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if str(group["matcher"]) != entry.Matcher {
			continue
		}
		inner := asAnyList(group["hooks"])
		for _, r := range inner {
			h, ok := r.(map[string]any)
			if !ok {
				continue
			}
			if str(h["command"]) == entry.Command {
				return i
			}
		}
	}
	return -1
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
