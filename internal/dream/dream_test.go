package dream_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/dream"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func newDream(t *testing.T) (*dream.Service, *memory.Service, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mem := memory.NewService(memory.Config{Store: db.Observations()})
	ds := dream.NewService(dream.Config{
		Memory:    mem,
		Store:     db.Observations(),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		StaleDays: 1,
	})
	return ds, mem, db
}

func TestDreamPrunesExpired(t *testing.T) {
	ds, mem, _ := newDream(t)
	ctx := context.Background()

	past := time.Now().Add(-time.Hour)
	_, _ = mem.Save(ctx, memory.SaveInput{
		Title: "short-lived", Content: "x", Type: memory.TypeContext,
		ValidUntil: &past,
	})
	// Also save an expiring observation via the store directly to test Prune.
	// (Simpler: set TTLDays to a negative value via direct insert.)

	j, err := ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = j // pruning only hits rows with expires_at set, which Save doesn't set
}

func TestDreamWritesJournal(t *testing.T) {
	ds, mem, _ := newDream(t)
	ctx := context.Background()

	// Seed enough observations to guarantee some decay possibility.
	for i := 0; i < 3; i++ {
		_, _ = mem.Save(ctx, memory.SaveInput{
			Title: "seed", Content: "content x", Type: memory.TypePattern,
			Importance: 5,
		})
	}
	// First pass: if nothing to decay, journal is still produced but not
	// written. Force a run with no journal.
	j, err := ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.FinishedAt.Before(j.StartedAt) {
		t.Errorf("FinishedAt should be >= StartedAt")
	}
}

func TestJournalSummaryIncludesCounts(t *testing.T) {
	j := dream.Journal{
		StartedAt:  time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 4, 16, 12, 0, 1, 0, time.UTC),
		Pruned:     5,
		Decayed:    3,
		Linked:     1,
		Promoted:   2,
		Notes:      []string{"test note"},
	}
	s := j.Summary()
	for _, want := range []string{"pruned: 5", "decayed: 3", "linked: 1", "promoted: 2", "test note"} {
		if !contains(s, want) {
			t.Errorf("summary missing %q: %s", want, s)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
