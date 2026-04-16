package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OpenAI is an embedder for OpenAI-compatible /v1/embeddings endpoints.
// Works with OpenAI proper, Together.ai, vLLM, LM Studio, etc.
type OpenAI struct {
	client    *http.Client
	baseURL   string
	apiKey    string
	model     string
	dimension int
}

// OpenAIConfig bundles OpenAI-compatible provider settings.
type OpenAIConfig struct {
	BaseURL   string // e.g. "https://api.openai.com/v1"
	APIKey    string // Authorization: Bearer <key>
	Model     string // e.g. "text-embedding-3-small"
	Dimension int    // e.g. 1536
	Timeout   time.Duration
}

// NewOpenAI constructs an OpenAI-compatible embedder.
func NewOpenAI(cfg OpenAIConfig) *OpenAI {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "text-embedding-3-small"
	}
	if cfg.Dimension == 0 {
		cfg.Dimension = 1536
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &OpenAI{
		client:    &http.Client{Timeout: cfg.Timeout},
		baseURL:   cfg.BaseURL,
		apiKey:    cfg.APIKey,
		model:     cfg.Model,
		dimension: cfg.Dimension,
	}
}

// Embed POSTs text to /v1/embeddings and returns the first vector.
func (o *OpenAI) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{
		"model":      o.model,
		"input":      text,
		"dimensions": o.dimension,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("openai: status %d", resp.StatusCode)
	}

	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openai: decode: %w", err)
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("openai: empty data array")
	}
	return out.Data[0].Embedding, nil
}

// Dimension returns the configured vector length.
func (o *OpenAI) Dimension() int { return o.dimension }

// Model returns a qualified identifier.
func (o *OpenAI) Model() string { return "openai/" + o.model }
