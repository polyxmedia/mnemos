package main

import (
	"context"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/embedding"
	"github.com/polyxmedia/mnemos/internal/memory"
)

// selectEmbedder resolves the configured embedder, auto-probing Ollama
// when provider="auto". Returns a memory.Embedder (the narrow interface
// the memory package consumes) to keep the import graph clean.
func selectEmbedder(ctx context.Context, cfg config.EmbeddingConfig) memory.Embedder {
	switch cfg.Provider {
	case "none":
		return embedding.NewNoop()
	case "ollama":
		return embedding.NewOllama(embedding.OllamaConfig{
			BaseURL:   cfg.BaseURL,
			Model:     cfg.Model,
			Dimension: cfg.Dimension,
		})
	case "openai":
		return embedding.NewOpenAI(embedding.OpenAIConfig{
			BaseURL:   cfg.BaseURL,
			APIKey:    cfg.APIKey,
			Model:     cfg.Model,
			Dimension: cfg.Dimension,
		})
	default: // "auto"
		if embedding.ProbeOllama(ctx, cfg.BaseURL) {
			return embedding.NewOllama(embedding.OllamaConfig{
				BaseURL:   cfg.BaseURL,
				Model:     cfg.Model,
				Dimension: cfg.Dimension,
			})
		}
		return embedding.NewNoop()
	}
}
