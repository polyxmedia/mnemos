// Package embedding provides pluggable embedding providers for hybrid
// retrieval. The consumer interface (Embedder) lives in internal/memory
// — idiomatic Go: interfaces declared where they are consumed, not where
// they are implemented. The types here (Ollama, OpenAI, Noop) implicitly
// satisfy memory.Embedder.
//
// Providers:
//   - Ollama (auto-detected; recommended for local, zero-cost use)
//   - OpenAI-compatible (any endpoint speaking /v1/embeddings)
//   - Noop (no-op fallback: returns nil vectors, disables vector search)
package embedding

import (
	"context"
	"errors"
)

// ErrNotConfigured signals that embedding is disabled. Callers can
// distinguish FTS5-only mode via errors.Is.
var ErrNotConfigured = errors.New("embedding: not configured")

// Noop is the zero-value embedder: returns nil vectors, dimension 0.
// Keeps the search path simple by always having a non-nil provider
// instead of scattered nil-checks.
type Noop struct{}

// NewNoop returns a no-op embedder.
func NewNoop() Noop { return Noop{} }

// Embed always returns nil — signals "no embedding available".
func (Noop) Embed(_ context.Context, _ string) ([]float32, error) { return nil, nil }

// Dimension returns 0 for the no-op provider.
func (Noop) Dimension() int { return 0 }

// Model returns "none".
func (Noop) Model() string { return "none" }
