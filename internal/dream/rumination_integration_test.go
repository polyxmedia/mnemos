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
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// TestDreamPassPersistsRuminationCandidates wires a full dream service
// with a real SQLite-backed rumination pipeline, saves a skill whose
// effectiveness is below the floor, runs the pass, and asserts the
// candidate ended up in the store with the expected provenance. This is
// the end-to-end seam test: every change to the dream, rumination, or
// storage layer that breaks persistence fires here before it breaks
// users.
func TestDreamPassPersistsRuminationCandidates(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	mem := memory.NewService(memory.Config{Store: db.Observations()})
	skl := skills.NewService(skills.Config{Store: db.Skills()})

	// Seed a skill that will fail the effectiveness monitor once we record
	// feedback: saved first with a procedure, then 12 unsuccessful uses.
	saved, err := skl.Save(ctx, skills.SaveInput{
		Name: "retry on 401", Description: "retry auth failures",
		Procedure: "retry the request", AgentID: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if err := skl.RecordUse(ctx, skills.FeedbackInput{ID: saved.ID, Success: false}); err != nil {
			t.Fatal(err)
		}
	}

	rum := rumination.NewService(rumination.Config{
		Monitors: []rumination.Monitor{
			&rumination.SkillEffectivenessMonitor{Skills: skl},
		},
		Skills: skl,
		Memory: db.Observations(),
		Store:  db.Rumination(),
	})

	ds := dream.NewService(dream.Config{
		Memory:     mem,
		Store:      db.Observations(),
		Reader:     db.Observations(),
		Skills:     skl,
		Rumination: rum,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		StaleDays:  365, // keep decay out of this test's scope
	})

	j, err := ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.RuminatedInserted != 1 {
		t.Fatalf("want 1 inserted candidate, got %d", j.RuminatedInserted)
	}
	if j.RuminatedUpdated != 0 {
		t.Errorf("first pass should not update, got %d", j.RuminatedUpdated)
	}

	pending, err := db.Rumination().Pending(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("store should have 1 pending candidate, got %d", len(pending))
	}
	got := pending[0]
	if got.TargetID != saved.ID {
		t.Errorf("candidate target = %s, want %s", got.TargetID, saved.ID)
	}
	if got.TargetKind != rumination.TargetSkill {
		t.Errorf("candidate kind = %s", got.TargetKind)
	}
	if got.Status != rumination.StatusPending {
		t.Errorf("candidate status = %s", got.Status)
	}
	if got.Severity < rumination.SeverityLow || got.Severity > rumination.SeverityHigh {
		t.Errorf("candidate severity out of range: %d", got.Severity)
	}

	// Second dream pass against the same failing skill: should update, not
	// re-insert. Idempotency is the contract.
	j2, err := ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j2.RuminatedInserted != 0 {
		t.Errorf("second pass should not insert, got %d", j2.RuminatedInserted)
	}
	if j2.RuminatedUpdated != 1 {
		t.Errorf("second pass should update existing candidate, got %d", j2.RuminatedUpdated)
	}
}

func TestDreamPassRuminationSkipsWhenNil(t *testing.T) {
	// Mirror: if no rumination service is configured, the dream pass runs
	// cleanly without touching the rumination counters. This is the
	// backwards-compat invariant — upgrading mnemos must not force anyone
	// to wire the new service just to keep dream working.
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mem := memory.NewService(memory.Config{Store: db.Observations()})
	ds := dream.NewService(dream.Config{
		Memory: mem,
		Store:  db.Observations(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	j, err := ds.Run(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if j.RuminatedInserted != 0 || j.RuminatedUpdated != 0 {
		t.Errorf("nil rumination service should leave counts at zero, got +%d/~%d",
			j.RuminatedInserted, j.RuminatedUpdated)
	}
}

// force-use to avoid unused-import lint before the extended test suite
var _ = time.Time{}
