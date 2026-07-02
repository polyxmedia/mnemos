package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// TestEfficacyAttributesOutcomes builds a fixed world of ended, open, and
// out-of-window sessions, then asserts the surfaced-vs-outcome aggregates.
// The exclusions (open session, out-of-window session) are encoded in the
// counts: if either leaked, prewarm surfacings would exceed 2.
func TestEfficacyAttributesOutcomes(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	now := time.Now().UTC()
	inWindow := now.Add(-time.Hour)
	outOfWindow := now.Add(-48 * time.Hour)
	cutoff := now.Add(-24 * time.Hour)

	// Two observations so ByMemory has titles to resolve and rank.
	obsStore := db.Observations()
	mkObs := func(id, title string) {
		t.Helper()
		if err := obsStore.Insert(ctx, &memory.Observation{
			ID: id, AgentID: "default", Project: "p", Title: title,
			Content: "c", Type: memory.TypeDecision, Importance: 5,
			CreatedAt: outOfWindow, ValidFrom: outOfWindow,
			SourceKind: memory.SourceUser, TrustTier: memory.TrustCurated,
		}); err != nil {
			t.Fatal(err)
		}
	}
	o1, o2 := ulid.Make().String(), ulid.Make().String()
	mkObs(o1, "wrap errors with %w")
	mkObs(o2, "never host on heroku")

	sessStore := db.Sessions()
	// A, B, C ended in-window; A/C ok, B failed. D open, E ended before window.
	mkEnded := func(status session.Status) string {
		t.Helper()
		id := ulid.Make().String()
		if err := sessStore.Insert(ctx, &session.Session{
			ID: id, AgentID: "default", Project: "p", StartedAt: inWindow,
		}); err != nil {
			t.Fatal(err)
		}
		if err := sessStore.Close(ctx, session.CloseInput{ID: id, Summary: "s", Status: status}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	sessA := mkEnded(session.StatusOK)
	sessB := mkEnded(session.StatusFailed)
	mkEnded(session.StatusOK) // sessC — no injections, lands in WithoutInjections.

	// D: open (never closed) — excluded from every aggregate.
	sessD := ulid.Make().String()
	if err := sessStore.Insert(ctx, &session.Session{
		ID: sessD, AgentID: "default", Project: "p", StartedAt: inWindow,
	}); err != nil {
		t.Fatal(err)
	}

	// E: ended before the window. Restore preserves ended_at verbatim, which
	// Close cannot (it always stamps now).
	sessE := ulid.Make().String()
	if _, err := sessStore.Restore(ctx, &session.Session{
		ID: sessE, AgentID: "default", Project: "p", Status: session.StatusOK,
		StartedAt: outOfWindow, EndedAt: &outOfWindow,
	}); err != nil {
		t.Fatal(err)
	}

	injStore := db.Injections()
	mkInj := func(sessionID, refID string, ch injection.Channel, created time.Time) injection.Event {
		return injection.Event{
			ID: ulid.Make().String(), Kind: injection.KindObservation,
			RefID: refID, Channel: ch, SessionID: sessionID, CreatedAt: created,
		}
	}
	if err := injStore.Record(ctx, []injection.Event{
		mkInj(sessA, o1, injection.ChannelPrewarm, inWindow),
		mkInj(sessA, o2, injection.ChannelPromptHook, inWindow),
		mkInj(sessB, o1, injection.ChannelPrewarm, inWindow),
		mkInj(sessD, o1, injection.ChannelPrewarm, inWindow),    // open session — excluded
		mkInj(sessE, o1, injection.ChannelPrewarm, outOfWindow), // ended pre-window — excluded
	}); err != nil {
		t.Fatal(err)
	}

	got, err := db.Efficacy(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}

	// Session split: A,B carried injections (1 of 2 ok); C did not (1 of 1 ok).
	if got.WithInjections.Sessions != 2 || got.WithInjections.OK != 1 {
		t.Errorf("with-injections: want 2 sessions / 1 ok, got %d / %d",
			got.WithInjections.Sessions, got.WithInjections.OK)
	}
	if got.WithoutInjections.Sessions != 1 || got.WithoutInjections.OK != 1 {
		t.Errorf("without-injections: want 1 session / 1 ok, got %d / %d",
			got.WithoutInjections.Sessions, got.WithoutInjections.OK)
	}

	// By channel: prewarm across A+B (1 ok), prompt_hook across A (1 ok).
	// D and E must not appear — their injections are excluded.
	byCh := map[string]storage.ChannelEfficacy{}
	for _, c := range got.ByChannel {
		byCh[c.Channel] = c
	}
	if len(got.ByChannel) != 2 {
		t.Fatalf("want 2 channels, got %d: %+v", len(got.ByChannel), got.ByChannel)
	}
	if pw := byCh["prewarm"]; pw.Surfacings != 2 || pw.Sessions != 2 || pw.OKSessions != 1 {
		t.Errorf("prewarm: want 2 surfacings / 2 sessions / 1 ok, got %+v", pw)
	}
	if ph := byCh["prompt_hook"]; ph.Surfacings != 1 || ph.Sessions != 1 || ph.OKSessions != 1 {
		t.Errorf("prompt_hook: want 1 surfacing / 1 session / 1 ok, got %+v", ph)
	}

	// By memory: o1 surfaced into A+B (2 sessions, 1 ok), o2 into A only.
	// o1 ranks first (more sessions). Titles resolve from observations.
	if len(got.ByMemory) != 2 {
		t.Fatalf("want 2 memories, got %d: %+v", len(got.ByMemory), got.ByMemory)
	}
	first := got.ByMemory[0]
	if first.RefID != o1 || first.Title != "wrap errors with %w" ||
		first.Surfacings != 2 || first.Sessions != 2 || first.OKSessions != 1 {
		t.Errorf("top memory: want o1 (2 surfacings / 2 sessions / 1 ok), got %+v", first)
	}
	second := got.ByMemory[1]
	if second.RefID != o2 || second.Title != "never host on heroku" ||
		second.Sessions != 1 || second.OKSessions != 1 {
		t.Errorf("second memory: want o2 (1 session / 1 ok), got %+v", second)
	}
}

// TestEfficacyEmptyStore proves a store with no ended sessions attributes
// nothing rather than erroring or fabricating a split.
func TestEfficacyEmptyStore(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	got, err := db.Efficacy(context.Background(), time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.WithInjections.Sessions != 0 || got.WithoutInjections.Sessions != 0 {
		t.Errorf("empty store must attribute no sessions, got %+v", got)
	}
	if len(got.ByChannel) != 0 || len(got.ByMemory) != 0 {
		t.Errorf("empty store must produce no channel/memory rows, got %+v / %+v",
			got.ByChannel, got.ByMemory)
	}
}
