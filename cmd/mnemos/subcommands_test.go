package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/config"
)

// homeWithConfig prepares an isolated $HOME and writes a config file that
// points at the given embedding provider + base_url. Returns the home dir.
func homeWithConfig(t *testing.T, embedConfig string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".mnemos")
	_ = os.MkdirAll(cfgDir, 0o755)
	if embedConfig != "" {
		_ = os.WriteFile(
			filepath.Join(cfgDir, "config.toml"),
			[]byte(embedConfig),
			0o644,
		)
	}
	return home
}

func TestRunDreamOneShot(t *testing.T) {
	homeWithConfig(t, "")
	out := captureStdout(t, func() {
		if err := runDream(context.Background(), nil); err != nil {
			t.Fatalf("dream: %v", err)
		}
	})
	if !strings.Contains(out, "dream pass") {
		t.Errorf("dream summary missing: %s", out)
	}
}

func TestRunVaultExport(t *testing.T) {
	home := homeWithConfig(t, "")
	vaultDir := filepath.Join(home, "myvault")
	out := captureStdout(t, func() {
		if err := runVaultExport(context.Background(), []string{"--out", vaultDir}); err != nil {
			t.Fatalf("vault export: %v", err)
		}
	})
	if !strings.Contains(out, "exported") {
		t.Errorf("vault export output: %s", out)
	}
	// The vault dir should now exist even if empty.
	if _, err := os.Stat(vaultDir); err != nil {
		t.Errorf("vault dir not created: %v", err)
	}
}

func TestRunEmbedStatusReturnsDisabled(t *testing.T) {
	homeWithConfig(t, `
[embedding]
provider = "none"
`)
	out := captureStdout(t, func() {
		_ = runEmbedStatus(context.Background())
	})
	if !strings.Contains(out, "enabled:  false") {
		t.Errorf("expected enabled: false, got: %s", out)
	}
}

func TestRunEmbedBackfillWithFakeOllama(t *testing.T) {
	// Fake Ollama endpoint that always returns a 4-dim vector.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" { // probe
			w.WriteHeader(200)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float32{{0.1, 0.2, 0.3, 0.4}},
		})
	}))
	defer srv.Close()

	homeWithConfig(t, `
[embedding]
provider = "ollama"
model = "test-model"
dimension = 4
base_url = "`+srv.URL+`"
`)

	out := captureStdout(t, func() {
		_ = runEmbedBackfill(context.Background())
	})
	if !strings.Contains(out, "backfilled") {
		t.Errorf("backfill output: %s", out)
	}
}

func TestSelectEmbedderVariants(t *testing.T) {
	tests := []struct {
		name      string
		cfg       config.EmbeddingConfig
		wantDim   int
		wantModel string
	}{
		{
			name:      "none",
			cfg:       config.EmbeddingConfig{Provider: "none"},
			wantDim:   0,
			wantModel: "none",
		},
		{
			name:      "ollama explicit",
			cfg:       config.EmbeddingConfig{Provider: "ollama", Model: "m", Dimension: 4},
			wantDim:   4,
			wantModel: "ollama/m",
		},
		{
			name:      "openai explicit",
			cfg:       config.EmbeddingConfig{Provider: "openai", Model: "x", Dimension: 8},
			wantDim:   8,
			wantModel: "openai/x",
		},
		{
			name:      "auto with no ollama",
			cfg:       config.EmbeddingConfig{Provider: "auto", BaseURL: "http://127.0.0.1:1"},
			wantDim:   0,
			wantModel: "none",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := selectEmbedder(context.Background(), tc.cfg)
			if e.Dimension() != tc.wantDim {
				t.Errorf("dim: want %d, got %d", tc.wantDim, e.Dimension())
			}
			if e.Model() != tc.wantModel {
				t.Errorf("model: want %q, got %q", tc.wantModel, e.Model())
			}
		})
	}
}

func TestRunSessionsEmpty(t *testing.T) {
	homeWithConfig(t, "")
	out := captureStdout(t, func() {
		_ = runSessions(context.Background(), nil)
	})
	if !strings.Contains(out, "no sessions yet") {
		t.Errorf("empty sessions output: %s", out)
	}
}

func TestRunVaultUnknownSubcommand(t *testing.T) {
	if err := runVault(context.Background(), []string{"badcmd"}); err == nil {
		t.Error("unknown vault subcommand must error")
	}
}

func TestRunEmbedUnknownSubcommand(t *testing.T) {
	if err := runEmbed(context.Background(), []string{"badcmd"}); err == nil {
		t.Error("unknown embed subcommand must error")
	}
	if err := runEmbed(context.Background(), nil); err == nil {
		t.Error("empty embed args must error")
	}
}

func TestRunVaultStatusBeforeExport(t *testing.T) {
	homeWithConfig(t, "")
	out := captureStdout(t, func() {
		_ = runVaultStatus(context.Background(), nil)
	})
	// Fresh config creates a vault path but nothing's been exported yet.
	if !strings.Contains(out, "vault path") {
		t.Errorf("expected vault path line, got: %s", out)
	}
}
