package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/config"
)

func TestDefaultReturnsSaneValues(t *testing.T) {
	d := config.Default()
	if d.Search.DecayRate != 0.05 {
		t.Errorf("DecayRate: want 0.05, got %v", d.Search.DecayRate)
	}
	if d.Search.MaxContextTokens != 2000 {
		t.Errorf("MaxContextTokens: want 2000, got %d", d.Search.MaxContextTokens)
	}
	if d.Server.Transport != "stdio" {
		t.Errorf("Transport: want stdio, got %q", d.Server.Transport)
	}
	if d.Embedding.Provider != "auto" {
		t.Errorf("Embedding provider: want auto, got %q", d.Embedding.Provider)
	}
	if d.Search.HybridAlpha != 0.5 {
		t.Errorf("HybridAlpha: want 0.5, got %v", d.Search.HybridAlpha)
	}
}

func TestLoadAutoCreatesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf", "mnemos.toml")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Path == "" {
		t.Error("expected default storage path")
	}
	// File should now exist.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("config file not created: %v", err)
	}
	// Should contain section headers.
	body, _ := os.ReadFile(path)
	for _, want := range []string{"[storage]", "[search]", "[server]", "[embedding]", "[vault]", "[dream]"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing section %s in written config: %s", want, body)
		}
	}
}

func TestLoadRespectsUserOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.toml")
	_ = os.WriteFile(path, []byte(`
[search]
decay_rate = 0.25
hybrid_alpha = 0.2

[server]
transport = "http"
http_addr = ":9000"

[embedding]
provider = "openai"
model = "text-embedding-3-small"
dimension = 1536
`), 0o644)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Search.DecayRate != 0.25 {
		t.Errorf("DecayRate override lost: %v", cfg.Search.DecayRate)
	}
	if cfg.Search.HybridAlpha != 0.2 {
		t.Errorf("HybridAlpha override lost: %v", cfg.Search.HybridAlpha)
	}
	if cfg.Server.Transport != "http" {
		t.Errorf("Transport override lost: %q", cfg.Server.Transport)
	}
	if cfg.Embedding.Provider != "openai" {
		t.Errorf("Embedding provider override lost: %q", cfg.Embedding.Provider)
	}
	// Defaults should still apply to unset fields.
	if cfg.Search.DefaultLimit != 20 {
		t.Errorf("DefaultLimit default not applied: %d", cfg.Search.DefaultLimit)
	}
}

func TestLoadRejectsInvalidTransport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.toml")
	_ = os.WriteFile(path, []byte(`
[server]
transport = "smoke-signals"
`), 0o644)

	if _, err := config.Load(path); err == nil {
		t.Error("expected validation error for invalid transport")
	}
}

func TestLoadRejectsBadHybridAlpha(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.toml")
	_ = os.WriteFile(path, []byte(`
[search]
hybrid_alpha = 2.5
`), 0o644)

	if _, err := config.Load(path); err == nil {
		t.Error("expected validation error for hybrid_alpha > 1")
	}
}

func TestLoadRejectsInvalidEmbeddingProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.toml")
	_ = os.WriteFile(path, []byte(`
[embedding]
provider = "cursed"
`), 0o644)
	if _, err := config.Load(path); err == nil {
		t.Error("expected validation error for unknown embedding provider")
	}
}

func TestLoadExpandsHomeInPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.toml")
	_ = os.WriteFile(path, []byte(`
[storage]
path = "~/my-mnemos.db"

[vault]
path = "~/vaults/mnemos"
`), 0o644)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(cfg.Storage.Path, "~") {
		t.Errorf("storage path not expanded: %q", cfg.Storage.Path)
	}
	if strings.HasPrefix(cfg.Vault.Path, "~") {
		t.Errorf("vault path not expanded: %q", cfg.Vault.Path)
	}
}

func TestDefaultPathPointsUnderHome(t *testing.T) {
	p := config.DefaultPath()
	if !strings.Contains(p, ".mnemos") {
		t.Errorf("DefaultPath should contain .mnemos: %s", p)
	}
}
