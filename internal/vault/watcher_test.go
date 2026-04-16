package vault_test

import (
	"context"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/vault"
)

func TestWatchStopsOnContextCancel(t *testing.T) {
	ex, _, _, _, _ := newVault(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- ex.Watch(ctx, 10*time.Millisecond) }()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not stop after cancel")
	}
}

func TestParseIntervalAccepts(t *testing.T) {
	cases := map[string]time.Duration{
		"":    0,
		"5m":  5 * time.Minute,
		"30s": 30 * time.Second,
		"1h":  time.Hour,
	}
	for input, want := range cases {
		got, err := vault.ParseInterval(input)
		if err != nil {
			t.Errorf("parse %q: %v", input, err)
		}
		if got != want {
			t.Errorf("parse %q: want %v, got %v", input, want, got)
		}
	}
}

func TestParseIntervalRejectsGarbage(t *testing.T) {
	if _, err := vault.ParseInterval("forever"); err == nil {
		t.Error("expected error on unparseable duration")
	}
}
