package storage_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func openRumDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// sampleCandidate returns a valid Candidate with fields dialled up the
// caller can override. Keeps test cases terse.
func sampleCandidate(id string) rumination.Candidate {
	return rumination.Candidate{
		ID:          id,
		MonitorName: "skill-effectiveness-floor",
		Severity:    rumination.SeverityMedium,
		Reason:      "effectiveness 0.20 after 15 uses (floor 0.30)",
		TargetKind:  rumination.TargetSkill,
		TargetID:    "sk-" + id,
		Evidence:    []rumination.Evidence{{Label: "history", Content: "3/15", Source: "sk-" + id}},
		DetectedAt:  time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC),
	}
}

func TestRumination_UpsertInsertsAndUpdates(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	ctx := context.Background()

	c := sampleCandidate("a")
	fresh, err := store.Upsert(ctx, c)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if !fresh {
		t.Errorf("first upsert should report fresh=true")
	}

	// Second upsert with the same dedup key (monitor, kind, target) but a
	// different severity should update, not insert.
	c2 := c
	c2.Severity = rumination.SeverityHigh
	c2.Reason = "now worse"
	c2.Evidence = append(c2.Evidence, rumination.Evidence{Label: "new", Content: "more"})
	fresh, err = store.Upsert(ctx, c2)
	if err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	if fresh {
		t.Errorf("second upsert of same (monitor,target) should be update, got fresh=true")
	}

	got, err := store.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Severity != rumination.SeverityHigh {
		t.Errorf("severity not updated: %s", got.Severity)
	}
	if got.Reason != "now worse" {
		t.Errorf("reason not updated: %s", got.Reason)
	}
	if len(got.Evidence) != 2 {
		t.Errorf("evidence not updated: %d items", len(got.Evidence))
	}
	// detected_at must be preserved even though severity was updated —
	// a repeat detection of the same issue is not a new issue.
	if !got.DetectedAt.Equal(c.DetectedAt) {
		t.Errorf("detected_at was modified on upsert: %v", got.DetectedAt)
	}
	if got.Status != rumination.StatusPending {
		t.Errorf("status should be pending on fresh candidate, got %s", got.Status)
	}
}

func TestRumination_UpsertDedupsPerMonitor(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	ctx := context.Background()

	a := sampleCandidate("a")
	b := sampleCandidate("b")
	b.MonitorName = "other-monitor"
	b.TargetID = a.TargetID // same target, different monitor → two rows

	if _, err := store.Upsert(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(ctx, b); err != nil {
		t.Fatal(err)
	}

	counts, err := store.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Pending != 2 {
		t.Errorf("different monitors should produce distinct rows, got %d pending", counts.Pending)
	}
}

func TestRumination_PendingOrdering(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	ctx := context.Background()

	// Insert low, high, medium in that order — Pending should return
	// high, medium, low.
	cLow := sampleCandidate("low")
	cLow.Severity = rumination.SeverityLow
	cHigh := sampleCandidate("high")
	cHigh.Severity = rumination.SeverityHigh
	cMed := sampleCandidate("med")
	cMed.Severity = rumination.SeverityMedium

	for _, c := range []rumination.Candidate{cLow, cHigh, cMed} {
		if _, err := store.Upsert(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	list, err := store.Pending(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("want 3, got %d", len(list))
	}
	wantOrder := []string{"high", "med", "low"}
	for i, id := range wantOrder {
		if list[i].ID != id {
			t.Errorf("Pending[%d] = %s, want %s", i, list[i].ID, id)
		}
	}

	// Limit applies.
	top, err := store.Pending(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || top[0].ID != "high" {
		t.Errorf("limit=1 should return highest severity: %+v", top)
	}
}

func TestRumination_PendingByTarget(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	ctx := context.Background()

	a := sampleCandidate("a")
	b := sampleCandidate("b")
	b.TargetID = "different-target"

	_, _ = store.Upsert(ctx, a)
	_, _ = store.Upsert(ctx, b)

	hits, err := store.PendingByTarget(ctx, rumination.TargetSkill, a.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "a" {
		t.Errorf("PendingByTarget mismatch: %+v", hits)
	}
}

func TestRumination_ResolveFlipsStatus(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	ctx := context.Background()

	c := sampleCandidate("a")
	if _, err := store.Upsert(ctx, c); err != nil {
		t.Fatal(err)
	}

	when := time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC)
	if err := store.Resolve(ctx, c.ID, "sk-new-version", when); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != rumination.StatusResolved {
		t.Errorf("status = %s, want resolved", got.Status)
	}
	if got.ResolvedBy != "sk-new-version" {
		t.Errorf("resolved_by = %s", got.ResolvedBy)
	}
	if !got.ResolvedAt.Equal(when) {
		t.Errorf("resolved_at = %v, want %v", got.ResolvedAt, when)
	}

	// Counts reflect the transition.
	counts, _ := store.Counts(ctx)
	if counts.Pending != 0 || counts.Resolved != 1 {
		t.Errorf("counts wrong after resolve: %+v", counts)
	}

	// Pending filter excludes resolved rows.
	list, _ := store.Pending(ctx, 0)
	if len(list) != 0 {
		t.Errorf("resolved row should not appear in Pending: %+v", list)
	}
}

func TestRumination_ResolveIdempotent(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	ctx := context.Background()

	c := sampleCandidate("a")
	_, _ = store.Upsert(ctx, c)
	when := time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC)
	if err := store.Resolve(ctx, c.ID, "sk-new", when); err != nil {
		t.Fatal(err)
	}
	// Same resolvedBy — no-op, not an error.
	if err := store.Resolve(ctx, c.ID, "sk-new", when.Add(time.Hour)); err != nil {
		t.Errorf("re-resolving with same resolved_by should succeed: %v", err)
	}
	// Different resolvedBy — conflict; caller should be told.
	if err := store.Resolve(ctx, c.ID, "sk-other", when); err == nil {
		t.Errorf("resolving already-resolved candidate with different revision should error")
	}
}

func TestRumination_ResolveRequiresProvenance(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	ctx := context.Background()

	if err := store.Resolve(ctx, "any", "", time.Now()); err == nil {
		t.Errorf("empty resolved_by should be rejected — provenance is the whole point")
	}
}

func TestRumination_ResolveMissing(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	ctx := context.Background()

	err := store.Resolve(ctx, "does-not-exist", "sk-x", time.Now())
	if !errors.Is(err, rumination.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestRumination_Dismiss(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	ctx := context.Background()

	c := sampleCandidate("a")
	_, _ = store.Upsert(ctx, c)

	when := time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC)
	if err := store.Dismiss(ctx, c.ID, "rule still applies; evidence was a one-off", when); err != nil {
		t.Fatal(err)
	}

	got, _ := store.Get(ctx, c.ID)
	if got.Status != rumination.StatusDismissed {
		t.Errorf("status = %s, want dismissed", got.Status)
	}
	if got.DismissedReason == "" {
		t.Errorf("dismissed_reason should be persisted")
	}
	if !got.DismissedAt.Equal(when) {
		t.Errorf("dismissed_at mismatch")
	}
}

func TestRumination_DismissRequiresReason(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	ctx := context.Background()

	c := sampleCandidate("a")
	_, _ = store.Upsert(ctx, c)

	if err := store.Dismiss(ctx, c.ID, "", time.Now()); err == nil {
		t.Errorf("empty reason should be rejected")
	}
}

func TestRumination_DismissOfNonPending(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	ctx := context.Background()

	c := sampleCandidate("a")
	_, _ = store.Upsert(ctx, c)
	_ = store.Resolve(ctx, c.ID, "sk-x", time.Now())

	if err := store.Dismiss(ctx, c.ID, "changed my mind", time.Now()); err == nil {
		t.Errorf("dismissing a resolved candidate should error")
	}
}

func TestRumination_CountsOnEmptyTable(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	counts, err := store.Counts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if counts.Pending != 0 || counts.Resolved != 0 || counts.Dismissed != 0 {
		t.Errorf("empty table should return zero counts, got %+v", counts)
	}
}

func TestRumination_UpsertValidation(t *testing.T) {
	db := openRumDB(t)
	store := db.Rumination()
	ctx := context.Background()

	bad := sampleCandidate("x")
	bad.ID = ""
	if _, err := store.Upsert(ctx, bad); err == nil {
		t.Errorf("missing id should be rejected")
	}

	bad = sampleCandidate("x")
	bad.MonitorName = ""
	if _, err := store.Upsert(ctx, bad); err == nil {
		t.Errorf("missing monitor should be rejected")
	}

	bad = sampleCandidate("x")
	bad.Severity = 0
	if _, err := store.Upsert(ctx, bad); err == nil {
		t.Errorf("severity=0 should be rejected")
	}

	bad = sampleCandidate("x")
	bad.TargetID = ""
	if _, err := store.Upsert(ctx, bad); err == nil {
		t.Errorf("missing target_id should be rejected")
	}
}
