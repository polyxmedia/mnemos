package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/api"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func newAPIServer(t *testing.T, apiKey string) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	srv := api.NewServer(api.Config{
		Memory:   memory.NewService(memory.Config{Store: db.Observations()}),
		Sessions: session.NewService(session.Config{Store: db.Sessions()}),
		Skills:   skills.NewService(skills.Config{Store: db.Skills()}),
		Touches:  db.Touches(),
		Prewarm: prewarm.NewService(prewarm.Config{
			Observations: db.Observations(),
			Sessions:     db.Sessions(),
			Skills:       db.Skills(),
			Touches:      db.Touches(),
		}),
		APIKey: apiKey,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func request(t *testing.T, ts *httptest.Server, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, ts.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestHealthzOpen(t *testing.T) {
	ts := newAPIServer(t, "secret")
	code, _ := request(t, ts, "GET", "/healthz", "", nil)
	if code != 200 {
		t.Errorf("healthz must be open, got %d", code)
	}
}

func TestAuthRequired(t *testing.T) {
	ts := newAPIServer(t, "secret")
	code, _ := request(t, ts, "POST", "/v1/search", "", map[string]any{"query": "x"})
	if code != 401 {
		t.Errorf("expected 401 without token, got %d", code)
	}
	code, _ = request(t, ts, "POST", "/v1/search", "secret",
		map[string]any{"query": "x"})
	if code != 200 {
		t.Errorf("expected 200 with token, got %d", code)
	}
}

func TestSaveSearchRoundTrip(t *testing.T) {
	ts := newAPIServer(t, "")
	code, out := request(t, ts, "POST", "/v1/observations", "", map[string]any{
		"title": "use WAL", "content": "concurrent readers", "type": "pattern",
	})
	if code != 201 {
		t.Fatalf("save failed: %d %+v", code, out)
	}
	observation := out["Observation"].(map[string]any)
	if observation["ID"] == nil {
		t.Error("expected ID in save result")
	}

	code, out = request(t, ts, "POST", "/v1/search", "",
		map[string]any{"query": "wal"})
	if code != 200 {
		t.Fatalf("search failed: %d %+v", code, out)
	}
	results := out["results"].([]any)
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSessionStartIncludesPrewarm(t *testing.T) {
	ts := newAPIServer(t, "")
	// Seed a convention.
	request(t, ts, "POST", "/v1/convention", "", map[string]any{
		"title": "error wrap", "rule": "use %w", "project": "mnemos",
	})
	code, out := request(t, ts, "POST", "/v1/sessions", "", map[string]any{
		"project": "mnemos", "goal": "ship",
	})
	if code != 201 {
		t.Fatalf("session start failed: %d %+v", code, out)
	}
	if out["prewarm"] == nil {
		t.Error("expected prewarm block in session_start response")
	}
}
