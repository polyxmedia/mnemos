// Package config loads Mnemos configuration from a TOML file with sensible
// defaults. The config is entirely optional — every field has a default,
// the file is auto-created on first run, and Mnemos works out of the box.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the fully-resolved Mnemos configuration.
type Config struct {
	Storage    StorageConfig    `toml:"storage"`
	Search     SearchConfig     `toml:"search"`
	Server     ServerConfig     `toml:"server"`
	Embedding  EmbeddingConfig  `toml:"embedding"`
	Vault      VaultConfig      `toml:"vault"`
	Dream      DreamConfig      `toml:"dream"`
	Rumination RuminationConfig `toml:"rumination"`
}

// StorageConfig controls the SQLite database location.
type StorageConfig struct {
	Path string `toml:"path"`
}

// SearchConfig tunes retrieval and ranking.
type SearchConfig struct {
	DecayRate        float64 `toml:"decay_rate"`
	DefaultLimit     int     `toml:"default_limit"`
	MaxContextTokens int     `toml:"max_context_tokens"`
	HybridAlpha      float64 `toml:"hybrid_alpha"`
}

// ServerConfig controls transport selection.
type ServerConfig struct {
	Transport string `toml:"transport"` // "stdio" or "http"
	HTTPAddr  string `toml:"http_addr"`
	APIKey    string `toml:"api_key"`
}

// EmbeddingConfig selects the embedding provider.
type EmbeddingConfig struct {
	Provider  string `toml:"provider"` // "auto" | "ollama" | "openai" | "none"
	Model     string `toml:"model"`
	Dimension int    `toml:"dimension"`
	BaseURL   string `toml:"base_url"`
	APIKey    string `toml:"api_key"`
}

// VaultConfig controls Obsidian vault export.
type VaultConfig struct {
	Enabled       bool   `toml:"enabled"`
	Path          string `toml:"path"`
	WatchInterval string `toml:"watch_interval"` // e.g. "5m"
}

// DreamConfig controls the consolidation daemon.
type DreamConfig struct {
	Interval    string `toml:"interval"` // e.g. "6h"; empty = off
	StaleDays   int    `toml:"stale_days"`
	DecayAmount int    `toml:"decay_amount"`
}

// RuminationConfig controls threshold-breach detection. Enabled by default
// so the dream pass starts flagging weak skills on its first run without
// additional user setup. Thresholds are conservative by design — the
// intent is to fire on statistically meaningful patterns, not on noise.
type RuminationConfig struct {
	Enabled                 bool    `toml:"enabled"`
	SkillEffectivenessFloor float64 `toml:"skill_effectiveness_floor"` // below this effectiveness → rumination candidate
	SkillMinUses            int     `toml:"skill_min_uses"`            // must have been used at least this many times first
	StaleSkillDays          int     `toml:"stale_skill_days"`          // skills untouched this many days and underperforming → candidate
	StaleSkillFloor         float64 `toml:"stale_skill_floor"`         // staleness triggers when effectiveness is below this
	CorrectionRepeatN       int     `toml:"correction_repeat_n"`       // N corrections on a topic *after* a matching skill was promoted → candidate
}

// Default returns the baked-in defaults — what you get on first run.
func Default() Config {
	return Config{
		Storage: StorageConfig{Path: defaultDBPath()},
		Search: SearchConfig{
			DecayRate:        0.05,
			DefaultLimit:     20,
			MaxContextTokens: 2000,
			HybridAlpha:      0.5,
		},
		Server: ServerConfig{
			Transport: "stdio",
			HTTPAddr:  ":8080",
		},
		Embedding: EmbeddingConfig{
			Provider:  "auto",
			Model:     "nomic-embed-text",
			Dimension: 768,
		},
		Vault: VaultConfig{
			Enabled:       false,
			Path:          defaultVaultPath(),
			WatchInterval: "5m",
		},
		Dream: DreamConfig{
			Interval:    "",
			StaleDays:   30,
			DecayAmount: 1,
		},
		Rumination: RuminationConfig{
			Enabled:                 true,
			SkillEffectivenessFloor: 0.3,
			SkillMinUses:            10,
			StaleSkillDays:          90,
			StaleSkillFloor:         0.5,
			CorrectionRepeatN:       3,
		},
	}
}

// Load reads config from path, applies defaults for any missing fields,
// expands ~ in paths, and validates. If path does not exist, the default
// config is written to it and returned.
func Load(path string) (Config, error) {
	cfg := Default()

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := ensureDir(filepath.Dir(path)); err != nil {
			return cfg, err
		}
		if err := write(path, cfg); err != nil {
			return cfg, fmt.Errorf("write default config: %w", err)
		}
	} else if err != nil {
		return cfg, fmt.Errorf("stat config: %w", err)
	} else {
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			return cfg, fmt.Errorf("decode %s: %w", path, err)
		}
	}

	applyDefaults(&cfg)
	cfg.Storage.Path = expandHome(cfg.Storage.Path)
	cfg.Vault.Path = expandHome(cfg.Vault.Path)

	if err := validate(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// DefaultPath returns the conventional config location (~/.mnemos/config.toml).
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "mnemos.toml"
	}
	return filepath.Join(home, ".mnemos", "config.toml")
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "mnemos.db"
	}
	return filepath.Join(home, ".mnemos", "mnemos.db")
}

func defaultVaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "mnemos-vault"
	}
	return filepath.Join(home, ".mnemos", "vault")
}

func expandHome(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}

func ensureDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func write(path string, cfg Config) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

func applyDefaults(cfg *Config) {
	d := Default()
	if cfg.Storage.Path == "" {
		cfg.Storage.Path = d.Storage.Path
	}
	if cfg.Search.DecayRate == 0 {
		cfg.Search.DecayRate = d.Search.DecayRate
	}
	if cfg.Search.DefaultLimit == 0 {
		cfg.Search.DefaultLimit = d.Search.DefaultLimit
	}
	if cfg.Search.MaxContextTokens == 0 {
		cfg.Search.MaxContextTokens = d.Search.MaxContextTokens
	}
	if cfg.Search.HybridAlpha == 0 {
		cfg.Search.HybridAlpha = d.Search.HybridAlpha
	}
	if cfg.Server.Transport == "" {
		cfg.Server.Transport = d.Server.Transport
	}
	if cfg.Server.HTTPAddr == "" {
		cfg.Server.HTTPAddr = d.Server.HTTPAddr
	}
	if cfg.Embedding.Provider == "" {
		cfg.Embedding.Provider = d.Embedding.Provider
	}
	if cfg.Embedding.Model == "" {
		cfg.Embedding.Model = d.Embedding.Model
	}
	if cfg.Embedding.Dimension == 0 {
		cfg.Embedding.Dimension = d.Embedding.Dimension
	}
	if cfg.Vault.Path == "" {
		cfg.Vault.Path = d.Vault.Path
	}
	if cfg.Vault.WatchInterval == "" {
		cfg.Vault.WatchInterval = d.Vault.WatchInterval
	}
	if cfg.Dream.StaleDays == 0 {
		cfg.Dream.StaleDays = d.Dream.StaleDays
	}
	if cfg.Dream.DecayAmount == 0 {
		cfg.Dream.DecayAmount = d.Dream.DecayAmount
	}
	if cfg.Rumination.SkillEffectivenessFloor == 0 {
		cfg.Rumination.SkillEffectivenessFloor = d.Rumination.SkillEffectivenessFloor
	}
	if cfg.Rumination.SkillMinUses == 0 {
		cfg.Rumination.SkillMinUses = d.Rumination.SkillMinUses
	}
	if cfg.Rumination.StaleSkillDays == 0 {
		cfg.Rumination.StaleSkillDays = d.Rumination.StaleSkillDays
	}
	if cfg.Rumination.StaleSkillFloor == 0 {
		cfg.Rumination.StaleSkillFloor = d.Rumination.StaleSkillFloor
	}
	if cfg.Rumination.CorrectionRepeatN == 0 {
		cfg.Rumination.CorrectionRepeatN = d.Rumination.CorrectionRepeatN
	}
}

func validate(cfg Config) error {
	if cfg.Storage.Path == "" {
		return errors.New("config: storage.path is empty")
	}
	switch cfg.Server.Transport {
	case "stdio", "http":
	default:
		return fmt.Errorf("config: server.transport must be 'stdio' or 'http', got %q", cfg.Server.Transport)
	}
	if cfg.Search.DefaultLimit < 1 {
		return errors.New("config: search.default_limit must be >= 1")
	}
	if cfg.Search.MaxContextTokens < 100 {
		return errors.New("config: search.max_context_tokens must be >= 100")
	}
	if cfg.Search.HybridAlpha < 0 || cfg.Search.HybridAlpha > 1 {
		return errors.New("config: search.hybrid_alpha must be in [0, 1]")
	}
	switch cfg.Embedding.Provider {
	case "auto", "ollama", "openai", "none":
	default:
		return fmt.Errorf("config: embedding.provider must be auto|ollama|openai|none, got %q", cfg.Embedding.Provider)
	}
	if cfg.Rumination.SkillEffectivenessFloor < 0 || cfg.Rumination.SkillEffectivenessFloor > 1 {
		return errors.New("config: rumination.skill_effectiveness_floor must be in [0, 1]")
	}
	if cfg.Rumination.SkillMinUses < 1 {
		return errors.New("config: rumination.skill_min_uses must be >= 1")
	}
	return nil
}
