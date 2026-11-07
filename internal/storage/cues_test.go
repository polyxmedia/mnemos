package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func insertObs(t *testing.T, db *storage.DB, id, title string, tier memory.TrustTier, created time.Time) {
	t.Helper()
	o := &memory.Observation{
		ID: id, AgentID: "default", Project: "mnemos", Title: title, Content: title,
		Type: memory.TypeConvention, Importance: 5, CreatedAt: created, ValidFrom: created,
		SourceKind: memory.SourceUser, TrustTier: tier,
	}
	if err := db.Observations().Insert(context.Background(), o); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func TestCuesAddForRemove(t *testing.T) {
	db := openInjDB(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	insertObs(t, db, "obs-1", "no force push", memory.TrustCurated, base)

	cues := db.Cues()
	if err := cues.Add(ctx, "obs-1", []string{"cmd:git push --force", "cmd:git push -f"}, base); err != nil {
		t.Fatal(err)
	}
	// Adding the same cue again is a no-op, not a duplicate row.
	if err := cues.Add(ctx, "obs-1", []string{"cmd:git push -f"}, base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := cues.For(ctx, "obs-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("For = %v, want two cues", got)
	}
	if err := cues.Remove(ctx, "obs-1", "cmd:git push -f"); err != nil {
		t.Fatal(err)
	}
	got, _ = cues.For(ctx, "obs-1")
	if len(got) != 1 || got[0] != "cmd:git push --force" {
		t.Fatalf("after Remove, For = %v", got)
	}
}

func TestCuesLiveSkipsDeadAndRawMemories(t *testing.T) {
	db := openInjDB(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	insertObs(t, db, "live", "live rule", memory.TrustCurated, base)
	insertObs(t, db, "dead", "dead rule", memory.TrustCurated, base)
	insertObs(t, db, "raw", "raw rule", memory.TrustRaw, base)
	for _, id := range []string{"live", "dead", "raw"} {
		if err := db.Cues().Add(ctx, id, []string{"cmd:git commit", "path:*.go"}, base); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Observations().Invalidate(ctx, "dead", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	got, err := db.Cues().Live(ctx, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Observation.ID != "live" {
		t.Fatalf("Live = %+v, want only the live curated memory", got)
	}
	if len(got[0].Cues) != 2 {
		t.Fatalf("Live grouped %v, want both cues on one memory", got[0].Cues)
	}
}

func TestRetrievalEventsRoundTrip(t *testing.T) {
	db := openInjDB(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	store := db.RetrievalEvents()

	long := make([]byte, 2000)
	for i := range long {
		long[i] = 'x'
	}
	events := []storage.RetrievalEvent{
		{CreatedAt: base, Channel: storage.EventPromptHook, HostSessionID: "host-1", Arm: "index",
			Query: string(long), TopScore: 4.2, Gap: 1.1,
			Candidates: []storage.ScoredRef{{ID: "a", Score: 4.2}, {ID: "b", Score: 3.1}}},
		{CreatedAt: base.Add(time.Millisecond), Channel: storage.EventCue, HostSessionID: "host-1",
			Arm: "index", Query: "git push --force", Fired: true, Injected: []string{"a"}, Chars: 180},
	}
	for _, e := range events {
		if err := store.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Record(ctx, storage.RetrievalEvent{Channel: storage.EventCue}); err == nil {
		t.Fatal("Record accepted an event without created_at")
	}

	got, err := store.Since(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Since returned %d events, want 2", len(got))
	}
	if len(got[0].Query) != 600 {
		t.Errorf("query stored with %d chars, want it cut to 600", len(got[0].Query))
	}
	if got[0].Fired || len(got[0].Candidates) != 2 || got[0].Candidates[1].ID != "b" {
		t.Errorf("first event = %+v", got[0])
	}
	if !got[1].Fired || got[1].Chars != 180 || len(got[1].Injected) != 1 {
		t.Errorf("second event = %+v", got[1])
	}
	if later, _ := store.Since(ctx, base.Add(time.Second)); len(later) != 0 {
		t.Errorf("Since after both events returned %d", len(later))
	}
}
