// Package installer wires Mnemos into agent clients (Claude Code, Cursor,
// Windsurf) by editing their MCP config files idempotently. It also powers
// `mnemos doctor` for self-diagnosis.
package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Target describes an MCP client config location and how to patch it. The
// Mnemos entry is keyed under Target.Key (usually "mnemos") inside an
// mcpServers object. We never rewrite unrelated keys.
type Target struct {
	Name string // human-readable (Claude Code, Cursor, ...)
	Path string // absolute path to the JSON config
	Key  string // server key under mcpServers
}

// ServerEntry is the value we write under mcpServers[Key].
type ServerEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// DetectTargets returns the MCP client config files that exist for the
// current user. Non-existent files are still returned if their parent
// directory is present, so `init` can create them idempotently.
func DetectTargets() []Target {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	candidates := []Target{
		{Name: "Claude Code (user)", Path: filepath.Join(home, ".claude.json"), Key: "mnemos"},
		{Name: "Cursor", Path: filepath.Join(home, ".cursor", "mcp.json"), Key: "mnemos"},
		{Name: "Windsurf", Path: filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), Key: "mnemos"},
	}
	out := make([]Target, 0, len(candidates))
	for _, c := range candidates {
		// Include the target if the file or its parent directory exists.
		// Parent existence means the client is installed; we can create the
		// file if needed.
		if _, err := os.Stat(c.Path); err == nil {
			out = append(out, c)
			continue
		}
		if _, err := os.Stat(filepath.Dir(c.Path)); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// Install patches the given target, adding (or updating) the mnemos entry
// under mcpServers. Unrelated keys are preserved. Returns true if the file
// was written (false if it was already up to date).
func Install(t Target, entry ServerEntry) (bool, error) {
	if err := ensureDir(filepath.Dir(t.Path)); err != nil {
		return false, err
	}

	cfg := map[string]any{}
	if data, err := os.ReadFile(t.Path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return false, fmt.Errorf("parse %s: %w", t.Path, err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", t.Path, err)
	}

	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}

	desired := map[string]any{"command": entry.Command}
	if len(entry.Args) > 0 {
		desired["args"] = entry.Args
	}
	if len(entry.Env) > 0 {
		desired["env"] = entry.Env
	}

	existing, _ := servers[t.Key].(map[string]any)
	if equalMaps(existing, desired) {
		return false, nil
	}
	servers[t.Key] = desired
	cfg["mcpServers"] = servers

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')
	if err := writeAtomic(t.Path, data); err != nil {
		return false, err
	}
	return true, nil
}

// Uninstall removes the mnemos entry from the target. Returns true if the
// file was changed.
func Uninstall(t Target) (bool, error) {
	data, err := os.ReadFile(t.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false, fmt.Errorf("parse: %w", err)
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if _, ok := servers[t.Key]; !ok {
		return false, nil
	}
	delete(servers, t.Key)
	cfg["mcpServers"] = servers
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	return true, writeAtomic(t.Path, append(out, '\n'))
}

// IsInstalled reports whether the target already has a mnemos entry.
func IsInstalled(t Target) bool {
	data, err := os.ReadFile(t.Path)
	if err != nil {
		return false
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	_, ok := servers[t.Key]
	return ok
}

func ensureDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func equalMaps(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}
