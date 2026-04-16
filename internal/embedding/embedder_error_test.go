package embedding_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/embedding"
)

func TestOllamaBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	e := embedding.NewOllama(embedding.OllamaConfig{BaseURL: srv.URL})
	if _, err := e.Embed(context.Background(), "text"); err == nil {
		t.Error("expected error on 500 response")
	}
}

func TestOpenAIBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()
	e := embedding.NewOpenAI(embedding.OpenAIConfig{BaseURL: srv.URL, APIKey: "k"})
	if _, err := e.Embed(context.Background(), "text"); err == nil {
		t.Error("expected error on 429 response")
	}
}

func TestProbeOllamaNotRunning(t *testing.T) {
	if embedding.ProbeOllama(context.Background(), "http://127.0.0.1:1") {
		t.Error("probe should return false for unreachable endpoint")
	}
}

func TestProbeOllamaSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !embedding.ProbeOllama(ctx, srv.URL) {
		t.Error("probe should succeed against 200-responding endpoint")
	}
}
