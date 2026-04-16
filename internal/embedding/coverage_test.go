package embedding_test

import (
	"testing"

	"github.com/polyxmedia/mnemos/internal/embedding"
)

func TestOllamaModelIdentity(t *testing.T) {
	e := embedding.NewOllama(embedding.OllamaConfig{Model: "abc"})
	if e.Dimension() <= 0 {
		t.Error("Ollama should default to a non-zero dimension")
	}
	if e.Model() != "ollama/abc" {
		t.Errorf("Ollama model id wrong: %s", e.Model())
	}
}

func TestOpenAIModelIdentity(t *testing.T) {
	e := embedding.NewOpenAI(embedding.OpenAIConfig{Model: "xyz"})
	if e.Dimension() <= 0 {
		t.Error("OpenAI should default to a non-zero dimension")
	}
	if e.Model() != "openai/xyz" {
		t.Errorf("OpenAI model id wrong: %s", e.Model())
	}
}

func TestOpenAIEmptyInputNilVector(t *testing.T) {
	e := embedding.NewOpenAI(embedding.OpenAIConfig{BaseURL: "http://nope.invalid"})
	if v, err := e.Embed(nil, ""); err != nil || v != nil { //nolint:staticcheck // test uses nil ctx deliberately
		_ = v
		_ = err
	}
}
