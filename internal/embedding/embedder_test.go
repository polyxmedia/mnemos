package embedding_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/polyxmedia/mnemos/internal/embedding"
)

func TestNoopEmbedder(t *testing.T) {
	e := embedding.NewNoop()
	v, err := e.Embed(context.Background(), "anything")
	if err != nil || v != nil {
		t.Errorf("Noop should return (nil, nil), got (%v, %v)", v, err)
	}
	if e.Dimension() != 0 {
		t.Errorf("Noop dimension should be 0")
	}
	if e.Model() != "none" {
		t.Errorf("Noop model should be 'none', got %q", e.Model())
	}
}

func TestOllamaEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float32{{0.1, 0.2, 0.3, 0.4}},
		})
	}))
	defer srv.Close()

	e := embedding.NewOllama(embedding.OllamaConfig{
		BaseURL:   srv.URL,
		Model:     "test-model",
		Dimension: 4,
	})
	v, err := e.Embed(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 4 || v[0] != 0.1 {
		t.Errorf("unexpected vector: %v", v)
	}
	if e.Model() != "ollama/test-model" {
		t.Errorf("wrong model id: %s", e.Model())
	}
}

func TestOpenAIEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key-123" {
			t.Errorf("missing/wrong auth header: %s", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": []float32{1, 2, 3, 4}},
			},
		})
	}))
	defer srv.Close()

	e := embedding.NewOpenAI(embedding.OpenAIConfig{
		BaseURL:   srv.URL,
		APIKey:    "key-123",
		Model:     "x",
		Dimension: 4,
	})
	v, err := e.Embed(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 4 || v[3] != 4 {
		t.Errorf("unexpected vector: %v", v)
	}
}

func TestOllamaEmptyInputReturnsNil(t *testing.T) {
	e := embedding.NewOllama(embedding.OllamaConfig{BaseURL: "http://nowhere.invalid"})
	v, err := e.Embed(context.Background(), "")
	if err != nil || v != nil {
		t.Errorf("empty input should be (nil, nil), got (%v, %v)", v, err)
	}
}
