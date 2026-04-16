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
	Storage StorageConfig `toml:"storage"`
	Search  SearchConfig  `toml:"search"`
	Server  ServerConfig  `toml:"server"`
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
}

// ServerConfig controls transport selection.
type ServerConfig struct {
	Transport string `toml:"transport"` // "stdio" or "http"
	HTTPAddr  string `toml:"http_addr"`
}

// Default returns the baked-in defaults — what you get on first run.
func Default() Config {
	return Config{
		Storage: StorageConfig{Path: defaultDBPath()},
		Search: SearchConfig{
			DecayRate:        0.05,
			DefaultLimit:     20,
			MaxContextTokens: 2000,
		},
		Server: ServerConfig{
			Transport: "stdio",
			HTTPAddr:  ":8080",
		},
	}
}

// Load reads config from path, applies defaults for any missing fields,
// expands ~ in the DB path, and validates. If path does not exist, the
// default config is written to it and returned.
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

	// Fill in any fields the file may have omitted.
	applyDefaults(&cfg)

	// Expand ~ in the DB path.
	if expanded, err := expandHome(cfg.Storage.Path); err == nil {
		cfg.Storage.Path = expanded
	}

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

func expandHome(p string) (string, error) {
	if len(p) == 0 || p[0] != '~' {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p, err
	}
	return filepath.Join(home, p[1:]), nil
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
	if cfg.Server.Transport == "" {
		cfg.Server.Transport = d.Server.Transport
	}
	if cfg.Server.HTTPAddr == "" {
		cfg.Server.HTTPAddr = d.Server.HTTPAddr
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
	return nil
}
