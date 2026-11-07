package memory_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func TestGetByHandle(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := memory.NewService(memory.Config{Store: db.Observations()})

	saved, err := svc.Save(ctx, memory.SaveInput{Title: "t", Content: "c", Type: memory.TypeDecision})
	if err != nil {
		t.Fatal(err)
	}
	id := saved.Observation.ID
	handle := memory.Handle(id)
	if len(handle) != memory.HandleLen || !strings.HasSuffix(id, handle) {
		t.Fatalf("handle %q of %q", handle, id)
	}

	for _, input := range []string{id, handle, strings.ToLower(handle)} {
		got, err := svc.Get(ctx, input)
		if err != nil {
			t.Errorf("Get(%q): %v", input, err)
			continue
		}
		if got.ID != id {
			t.Errorf("Get(%q) = %s, want %s", input, got.ID, id)
		}
	}
	got, err := svc.Get(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessCount < 2 {
		t.Errorf("opening by handle should bump access like an ID does: %d", got.AccessCount)
	}

	if _, err := svc.Get(ctx, "ZZZZZZZZ"); !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("unknown handle: %v", err)
	}
}

func TestGetByHandleRefusesAnAmbiguousMatch(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := db.Observations()
	now := time.Now().UTC()
	for _, id := range []string{"01AAAAAAAAAAAAAAAASAMETAIL", "01BBBBBBBBBBBBBBBBSAMETAIL"} {
		added, err := store.Restore(ctx, &memory.Observation{
			ID: id, Title: "t" + id, Content: "c", Type: memory.TypeDecision, AgentID: "default",
			ContentHash: id, Importance: 5, CreatedAt: now, ValidFrom: now,
		})
		if err != nil || !added {
			t.Fatalf("restore %s: added=%v err=%v", id, added, err)
		}
	}
	svc := memory.NewService(memory.Config{Store: store})
	if _, err := svc.Get(ctx, "SAMETAIL"); !errors.Is(err, memory.ErrAmbiguousHandle) {
		t.Errorf("want ErrAmbiguousHandle, got %v", err)
	}
}

func TestGetPrefersAnExactIDOverAHandle(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := db.Observations()
	now := time.Now().UTC()
	for _, id := range []string{"lower123", "01AAAAAAAAAAAAAAAALOWER123"} {
		added, err := store.Restore(ctx, &memory.Observation{
			ID: id, Title: "t" + id, Content: "c", Type: memory.TypeDecision, AgentID: "default",
			ContentHash: id, Importance: 5, CreatedAt: now, ValidFrom: now,
		})
		if err != nil || !added {
			t.Fatalf("restore %s: added=%v err=%v", id, added, err)
		}
	}
	svc := memory.NewService(memory.Config{Store: store})
	got, err := svc.Get(ctx, "lower123")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "lower123" {
		t.Errorf("exact ID should win, got %s", got.ID)
	}
}
