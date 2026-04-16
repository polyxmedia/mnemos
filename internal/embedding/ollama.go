package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Ollama is an embedder that talks to a local Ollama daemon. Free, local,
// zero config — the default when Ollama is detected on startup.
type Ollama struct {
	client    *http.Client
	baseURL   string
	model     string
	dimension int
}

// OllamaConfig bundles Ollama provider settings.
type OllamaConfig struct {
	BaseURL   string        // e.g. "http://localhost:11434"
	Model     string        // e.g. "nomic-embed-text"
	Dimension int           // e.g. 768 (matches the model)
	Timeout   time.Duration // request timeout
}

// NewOllama constructs an Ollama embedder with the given config. It does
// not probe the server — use ProbeOllama first if you need to confirm
// availability before wiring.
func NewOllama(cfg OllamaConfig) *Ollama {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:11434"
	}
	if cfg.Model == "" {
		cfg.Model = "nomic-embed-text"
	}
	if cfg.Dimension == 0 {
		cfg.Dimension = 768
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Ollama{
		client:    &http.Client{Timeout: cfg.Timeout},
		baseURL:   cfg.BaseURL,
		model:     cfg.Model,
		dimension: cfg.Dimension,
	}
}

// Embed sends text to Ollama's /api/embed endpoint and returns the vector.
func (o *Ollama) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{
		"model": o.model,
		"input": text,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("ollama: status %d", resp.StatusCode)
	}

	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ollama: decode: %w", err)
	}
	if len(out.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama: empty embeddings response")
	}
	return out.Embeddings[0], nil
}

// Dimension returns the configured vector length.
func (o *Ollama) Dimension() int { return o.dimension }

// Model returns the Ollama model name.
func (o *Ollama) Model() string { return "ollama/" + o.model }

// ProbeOllama returns true if an Ollama daemon is responding at baseURL.
// Used at startup to auto-enable the provider without user config.
func ProbeOllama(ctx context.Context, baseURL string) bool {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, baseURL+"/", nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 500 * time.Millisecond}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
