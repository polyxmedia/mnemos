package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/pkg/client"
)

func TestGetSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(client.Observation{ID: "abc", Title: "x"})
	}))
	defer ts.Close()
	c := client.New(ts.URL)
	o, err := c.Get(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if o.ID != "abc" {
		t.Errorf("wrong id: %s", o.ID)
	}
}

func TestDeleteReturnsNoContent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("wrong method: %s", r.Method)
		}
		w.WriteHeader(204)
	}))
	defer ts.Close()
	c := client.New(ts.URL)
	if err := c.Delete(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}
}

func TestSearchReturnsResults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []client.SearchHit{{Score: 1.0, Snippet: "x"}},
		})
	}))
	defer ts.Close()
	c := client.New(ts.URL)
	hits, err := c.Search(context.Background(), client.SearchInput{Query: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Errorf("want 1 hit, got %d", len(hits))
	}
}

func TestSessionStartReturnsPrewarm(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_id": "s1",
			"started_at": time.Now().Format(time.RFC3339Nano),
			"prewarm":    map[string]any{"Text": "hello", "TokenEstimate": 5},
		})
	}))
	defer ts.Close()
	c := client.New(ts.URL)
	res, err := c.SessionStart(context.Background(), client.SessionStartInput{Goal: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "s1" || res.Prewarm == nil || res.Prewarm.Text != "hello" {
		t.Errorf("unexpected: %+v", res)
	}
}

func TestSessionEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions/s1/close" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()
	c := client.New(ts.URL)
	if err := c.SessionEnd(context.Background(), "s1", "summary", "reflection", "ok"); err != nil {
		t.Fatal(err)
	}
}

func TestTransportError(t *testing.T) {
	c := client.New("http://127.0.0.1:1")
	_, err := c.Get(context.Background(), "abc")
	if err == nil {
		t.Error("expected transport error")
	}
}

func TestWithTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer ts.Close()
	c := client.New(ts.URL, client.WithTimeout(10*time.Millisecond))
	err := c.Healthz(context.Background())
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestAPIErrorMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer ts.Close()
	c := client.New(ts.URL)
	_, err := c.Get(context.Background(), "abc")
	if err == nil || !contains(err.Error(), "500") {
		t.Errorf("expected 500 in error message, got %v", err)
	}
}

func TestWithHTTPClientOption(t *testing.T) {
	custom := &http.Client{Timeout: time.Second}
	c := client.New("http://x.invalid", client.WithHTTPClient(custom))
	_ = c // just exercise the option path
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
