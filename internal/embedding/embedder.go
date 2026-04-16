// Package embedding provides pluggable embedding providers used by the
// hybrid search layer. Embeddings are optional — when no provider is
// configured, Mnemos runs pure FTS5 and still works.
//
// Supported providers:
//   - Ollama (auto-detected; recommended for local, zero-cost use)
//   - OpenAI-compatible (any endpoint that speaks /v1/embeddings)
//   - Noop (fallback: returns nil vectors, disables vector search)
package embedding

import (
	"context"
	"errors"
)

// Embedder turns text into a fixed-length float32 vector. Implementations
// MUST return vectors of the same Dimension on every call.
type Embedder interface {
	// Embed returns a vector for text. Empty input returns (nil, nil).
	Embed(ctx context.Context, text string) ([]float32, error)

	// Dimension returns the vector length this embedder produces.
	Dimension() int

	// Model returns the model identifier (e.g. "nomic-embed-text"). Used
	// so we can re-embed when the model changes.
	Model() string
}

// ErrNotConfigured signals that embedding is disabled (no-op provider).
// Callers can use errors.Is(err, ErrNotConfigured) to detect the
// FTS5-only mode gracefully.
var ErrNotConfigured = errors.New("embedding: not configured")

// Noop is a fall-through embedder that returns nil vectors. Used when
// no provider is configured; keeps the search path simple by always
// having a non-nil Embedder.
type Noop struct{}

// NewNoop returns a no-op embedder.
func NewNoop() Embedder { return Noop{} }

// Embed always returns nil — signals "no embedding available".
func (Noop) Embed(_ context.Context, _ string) ([]float32, error) { return nil, nil }

// Dimension returns 0 for the no-op provider.
func (Noop) Dimension() int { return 0 }

// Model returns "none".
func (Noop) Model() string { return "none" }
