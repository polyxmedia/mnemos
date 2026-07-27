package memory_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// TestRecordSurfacedBumpsAccessCount guards the signal that had never once
// fired in production: access_count only moved on an explicit Get, which no
// hook path calls, so Ranker.Score's access term was a constant 1.0 for
// every stored memory.
func TestRecordSurfacedBumpsAccessCount(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := memory.NewService(memory.Config{Store: db.Observations()})

	saved, err := svc.Save(ctx, memory.SaveInput{
		Title:   "WAL mode for sqlite",
		Content: "enable WAL so readers do not block the writer",
		Type:    memory.TypeDecision,
		Project: "mnemos",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := saved.Observation.ID

	if saved.Observation.AccessCount != 0 {
		t.Fatalf("fresh memory should start at 0, got %d", saved.Observation.AccessCount)
	}

	if err := svc.RecordSurfaced(ctx, []string{id, id, ""}); err != nil {
		t.Fatalf("record surfaced: %v", err)
	}

	// Export, not Get: Get bumps access itself, so asserting through it
	// would measure the assertion rather than the change under test.
	got, err := db.Observations().Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found *memory.Observation
	for i := range got {
		if got[i].ID == id {
			found = &got[i]
		}
	}
	if found == nil {
		t.Fatalf("saved memory %s not found", id)
	}
	if found.AccessCount != 2 {
		t.Errorf("want 2 bumps for two surfacings, got %d", found.AccessCount)
	}
}

func TestRecordSurfacedIgnoresEmptyInput(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := memory.NewService(memory.Config{Store: db.Observations()})
	if err := svc.RecordSurfaced(ctx, nil); err != nil {
		t.Errorf("nil ids must be a no-op, got %v", err)
	}
	if err := svc.RecordSurfaced(ctx, []string{"", ""}); err != nil {
		t.Errorf("blank ids must be skipped, got %v", err)
	}
}
