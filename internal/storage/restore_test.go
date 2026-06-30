package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

func eqTime(t *testing.T, label string, got, want time.Time) {
	t.Helper()
	if !got.UTC().Equal(want.UTC()) {
		t.Errorf("%s: got %v, want %v", label, got.UTC(), want.UTC())
	}
}

func TestRestoreObservationPreservesFidelity(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	ctx := context.Background()

	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	validUntil := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	invalidated := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	expires := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	accessed := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	want := &memory.Observation{
		ID: "obs_fixture_1", AgentID: "agentX", Project: "projX",
		Title: "retry 401", Content: "401 is auth, refresh the token",
		Type: memory.TypeCorrection, Tags: []string{"auth", "http"},
		Importance: 8, AccessCount: 7, LastAccessedAt: &accessed,
		CreatedAt: t0, ValidFrom: t0, ValidUntil: &validUntil,
		InvalidatedAt: &invalidated, ExpiresAt: &expires,
		ContentHash: "deadbeef", Structured: `{"tried":"retry"}`,
		Rationale:  "auth failures are not transient",
		SourceKind: memory.SourceTool, TrustTier: memory.TrustRaw,
		DerivedFrom: []string{"p1", "p2"},
	}

	added, err := store.Restore(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("first restore should insert a row")
	}

	got, err := store.Get(ctx, want.ID)
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != want.ID || got.AgentID != want.AgentID || got.Project != want.Project {
		t.Errorf("identity/scope not preserved: id=%q agent=%q project=%q", got.ID, got.AgentID, got.Project)
	}
	if got.Type != memory.TypeCorrection || got.Importance != 8 || got.AccessCount != 7 {
		t.Errorf("type/importance/access not preserved: type=%v imp=%d acc=%d", got.Type, got.Importance, got.AccessCount)
	}
	if got.SourceKind != memory.SourceTool || got.TrustTier != memory.TrustRaw {
		t.Errorf("provenance not preserved: source_kind=%v trust_tier=%v", got.SourceKind, got.TrustTier)
	}
	if got.ContentHash != "deadbeef" {
		t.Errorf("content_hash not preserved: %q", got.ContentHash)
	}
	if len(got.DerivedFrom) != 2 || got.DerivedFrom[0] != "p1" || got.DerivedFrom[1] != "p2" {
		t.Errorf("derived_from not preserved: %v", got.DerivedFrom)
	}
	if got.InvalidatedAt == nil || got.ValidUntil == nil || got.ExpiresAt == nil || got.LastAccessedAt == nil {
		t.Fatalf("bi-temporal pointers lost: %+v", got)
	}
	eqTime(t, "created_at", got.CreatedAt, t0)
	eqTime(t, "valid_from", got.ValidFrom, t0)
	eqTime(t, "valid_until", *got.ValidUntil, validUntil)
	eqTime(t, "invalidated_at", *got.InvalidatedAt, invalidated)
	eqTime(t, "expires_at", *got.ExpiresAt, expires)

	// Idempotent: re-restoring the same ID is skipped, not duplicated.
	added2, err := store.Restore(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if added2 {
		t.Error("second restore of the same ID must be skipped")
	}
}

func TestRestoreSessionPreservesFidelity(t *testing.T) {
	db := openTestDB(t)
	sess := db.Sessions()
	ctx := context.Background()

	started := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	ended := time.Date(2026, 1, 1, 17, 0, 0, 0, time.UTC)
	want := &session.Session{
		ID: "sess_fixture_1", AgentID: "agentX", Project: "projX",
		Goal: "ship the feature", Summary: "shipped, one bug found",
		Reflection: "write the test earlier next time",
		Status:     session.StatusFailed, OutcomeTags: []string{"shipped", "buggy"},
		StartedAt: started, EndedAt: &ended,
	}

	added, err := sess.Restore(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("first restore should insert a row")
	}

	got, err := sess.Get(ctx, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != session.StatusFailed || got.Summary != want.Summary || got.Reflection != want.Reflection {
		t.Errorf("session fields not preserved: status=%v summary=%q reflection=%q", got.Status, got.Summary, got.Reflection)
	}
	if got.EndedAt == nil {
		t.Fatal("ended_at lost")
	}
	eqTime(t, "started_at", got.StartedAt, started)
	eqTime(t, "ended_at", *got.EndedAt, ended)

	if added2, _ := sess.Restore(ctx, want); added2 {
		t.Error("second restore of the same session must be skipped")
	}
}

func TestRestoreSkillPreservesFidelity(t *testing.T) {
	db := openTestDB(t)
	skl := db.Skills()
	ctx := context.Background()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	want := &skills.Skill{
		ID: "skill_fixture_1", AgentID: "agentX", Name: "oauth-retry",
		Description: "retry oauth once", Procedure: "refresh token, then retry once",
		Pitfalls: "do not loop forever", Tags: []string{"auth"},
		SourceSessions: []string{"s1"},
		UseCount:       9, SuccessCount: 7, Effectiveness: 0.78, Version: 4,
		CreatedAt: t0, UpdatedAt: t0,
	}

	added, err := skl.Restore(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("first restore should insert a row")
	}

	got, err := skl.Get(ctx, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 4 || got.UseCount != 9 || got.SuccessCount != 7 {
		t.Errorf("skill counters/version not preserved: version=%d use=%d success=%d", got.Version, got.UseCount, got.SuccessCount)
	}
	if got.Effectiveness < 0.77 || got.Effectiveness > 0.79 {
		t.Errorf("effectiveness not preserved: %v", got.Effectiveness)
	}

	if added2, _ := skl.Restore(ctx, want); added2 {
		t.Error("second restore of the same skill must be skipped")
	}
}
