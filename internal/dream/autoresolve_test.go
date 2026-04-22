package dream_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/dream"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// TestDreamAutoResolvesViaProvenanceTag is the closing-the-loop
// end-to-end test. A skill is seeded with low effectiveness, the dream
// pass raises a candidate, then a new skill version is saved with the
// ruminated-from:<id> tag. The next dream pass must auto-close the
// candidate without the agent having called mnemos_ruminate_resolve.
func TestDreamAutoResolvesViaProvenanceTag(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	mem := memory.NewService(memory.Config{Store: db.Observations()})
	skl := skills.NewService(skills.Config{Store: db.Skills()})
	rum := rumination.NewService(rumination.Config{
		Monitors: []rumination.Monitor{&rumination.SkillEffectivenessMonitor{Skills: skl}},
		Skills:   skl,
		Memory:   db.Observations(),
		Store:    db.Rumination(),
	})
	ds := dream.NewService(dream.Config{
		Memory: mem, Store: db.Observations(), Reader: db.Observations(),
		Skills: skl, Rumination: rum,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	saved, err := skl.Save(ctx, skills.SaveInput{
		Name: "retry on 401", Description: "retry auth failures",
		Procedure: "retry the request",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if err := skl.RecordUse(ctx, skills.FeedbackInput{ID: saved.ID, Success: false}); err != nil {
			t.Fatal(err)
		}
	}

	// First pass: candidate surfaced.
	j1, err := ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j1.RuminatedInserted != 1 {
		t.Fatalf("want 1 candidate raised, got %d", j1.RuminatedInserted)
	}
	pending, _ := db.Rumination().Pending(ctx, 0)
	if len(pending) != 1 {
		t.Fatalf("want 1 pending candidate, got %d", len(pending))
	}
	candID := pending[0].ID

	// Agent writes a revised skill with the provenance tag. Same name,
	// so the skill versions up — but our tag scan runs over the current
	// version, so the tag is visible next dream pass.
	_, err = skl.Save(ctx, skills.SaveInput{
		Name: "retry on 401", Description: "retry auth failures (revised)",
		Procedure: "refresh the token first, then retry exactly once",
		Tags:      []string{"ruminated-from:" + candID},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Second pass: auto-resolve closes the candidate.
	j2, err := ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j2.RuminatedResolved != 1 {
		t.Errorf("want 1 auto-resolved candidate, got %d", j2.RuminatedResolved)
	}

	// Verify the candidate transitioned cleanly.
	got, err := db.Rumination().Get(ctx, candID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != rumination.StatusResolved {
		t.Errorf("candidate status = %s, want resolved", got.Status)
	}
	if got.ResolvedBy != saved.ID {
		t.Errorf("candidate resolved_by = %s, want %s", got.ResolvedBy, saved.ID)
	}

	// Third pass: same revision, already resolved; RuminatedResolved must
	// stay at zero. This is the idempotency guard on the dream side.
	j3, err := ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j3.RuminatedResolved != 0 {
		t.Errorf("already-resolved candidate should not re-resolve, got %d", j3.RuminatedResolved)
	}
}

func TestDreamAutoResolveIgnoresUnrelatedTags(t *testing.T) {
	// A skill saved with tags that don't match the ruminated-from prefix
	// must not trigger any resolve calls. Protects against a future
	// tag-prefix naming collision.
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	mem := memory.NewService(memory.Config{Store: db.Observations()})
	skl := skills.NewService(skills.Config{Store: db.Skills()})
	rum := rumination.NewService(rumination.Config{
		Skills: skl, Memory: db.Observations(), Store: db.Rumination(),
	})
	ds := dream.NewService(dream.Config{
		Memory: mem, Store: db.Observations(), Reader: db.Observations(),
		Skills: skl, Rumination: rum,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	_, _ = skl.Save(ctx, skills.SaveInput{
		Name: "unrelated", Description: "d", Procedure: "p",
		Tags: []string{"auto-promoted", "project:x"},
	})

	j, err := ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.RuminatedResolved != 0 {
		t.Errorf("unrelated tags should not trigger auto-resolve, got %d", j.RuminatedResolved)
	}
}

func TestDreamAutoResolveSkipsMissingCandidate(t *testing.T) {
	// A skill carrying a ruminated-from tag whose candidate doesn't exist
	// (agent typo, or candidate was hard-deleted) should not fail the
	// pass. Silent skip is the contract.
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	mem := memory.NewService(memory.Config{Store: db.Observations()})
	skl := skills.NewService(skills.Config{Store: db.Skills()})
	rum := rumination.NewService(rumination.Config{
		Skills: skl, Memory: db.Observations(), Store: db.Rumination(),
	})
	ds := dream.NewService(dream.Config{
		Memory: mem, Store: db.Observations(), Reader: db.Observations(),
		Skills: skl, Rumination: rum,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	_, _ = skl.Save(ctx, skills.SaveInput{
		Name: "ghost ref", Description: "d", Procedure: "p",
		Tags: []string{"ruminated-from:rumination-does-not-exist"},
	})

	j, err := ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.RuminatedResolved != 0 {
		t.Errorf("missing candidate should be silent skip, got %d", j.RuminatedResolved)
	}
}
