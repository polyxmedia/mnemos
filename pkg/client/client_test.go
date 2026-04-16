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

func TestSaveRoundTrip(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/observations" || r.Method != "POST" {
			t.Errorf("wrong route: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Observation": map[string]any{
				"ID":        "01HXYZ",
				"Title":     "t",
				"Type":      "pattern",
				"CreatedAt": time.Now(),
			},
			"Deduped": false,
		})
	}))
	defer ts.Close()

	c := client.New(ts.URL)
	res, err := c.Save(context.Background(), client.SaveInput{
		Title: "t", Content: "c", Type: "pattern",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Observation.ID != "01HXYZ" {
		t.Errorf("wrong ID: %s", res.Observation.ID)
	}
}

func TestAuthHeader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer abc" {
			t.Errorf("missing auth header: %s", r.Header.Get("Authorization"))
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	c := client.New(ts.URL, client.WithAPIKey("abc"))
	if err := c.Healthz(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIsNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.Get(context.Background(), "missing")
	if !client.IsNotFound(err) {
		t.Errorf("expected IsNotFound, got %v", err)
	}
}
