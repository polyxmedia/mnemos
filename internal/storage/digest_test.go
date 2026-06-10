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

func TestDigestAggregatesActivity(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	now := time.Now().UTC()
	inWindow := now.Add(-time.Hour)
	outOfWindow := now.Add(-48 * time.Hour)

	// Observations: two in-window (correction + decision), one old.
	obsStore := db.Observations()
	mkObs := func(id, title string, typ memory.ObsType, created time.Time) {
		t.Helper()
		if err := obsStore.Insert(ctx, &memory.Observation{
			ID: id, AgentID: "default", Project: "p", Title: title,
			Content: "c", Type: typ, Importance: 5,
			CreatedAt: created, ValidFrom: created,
			SourceKind: memory.SourceUser, TrustTier: memory.TrustCurated,
		}); err != nil {
			t.Fatal(err)
		}
	}
	freshCorrection := ulid.Make().String()
	mkObs(freshCorrection, "fresh correction", memory.TypeCorrection, inWindow)
	mkObs(ulid.Make().String(), "fresh decision", memory.TypeDecision, inWindow)
	mkObs(ulid.Make().String(), "old decision", memory.TypeDecision, outOfWindow)

	// Sessions: one opened+closed in window, one old.
	sessStore := db.Sessions()
	fresh := &session.Session{ID: ulid.Make().String(), AgentID: "default", Project: "p", StartedAt: inWindow}
	old := &session.Session{ID: ulid.Make().String(), AgentID: "default", Project: "p", StartedAt: outOfWindow}
	for _, s := range []*session.Session{fresh, old} {
		if err := sessStore.Insert(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := sessStore.Close(ctx, session.CloseInput{ID: fresh.ID, Summary: "done", Status: session.StatusFailed}); err != nil {
		t.Fatal(err)
	}

	// Injections: the fresh correction surfaced twice in window, once out.
	injStore := db.Injections()
	mkInj := func(created time.Time, channel injection.Channel) injection.Event {
		return injection.Event{
			ID: ulid.Make().String(), Kind: injection.KindObservation,
			RefID: freshCorrection, Channel: channel, CreatedAt: created,
		}
	}
	if err := injStore.Record(ctx, []injection.Event{
		mkInj(inWindow, injection.ChannelPrewarm),
		mkInj(inWindow.Add(time.Minute), injection.ChannelPromptHook),
		mkInj(outOfWindow, injection.ChannelPrewarm),
	}); err != nil {
		t.Fatal(err)
	}

	got, err := db.Digest(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	if got.ObservationsSaved != 2 {
		t.Errorf("observations saved: want 2, got %d", got.ObservationsSaved)
	}
	if got.SessionsOpened != 1 || got.SessionsClosed != 1 {
		t.Errorf("sessions: want 1 opened / 1 closed, got %d / %d", got.SessionsOpened, got.SessionsClosed)
	}
	if len(got.SessionsByStatus) != 1 || got.SessionsByStatus[0].Kind != "failed" {
		t.Errorf("want failed status breakdown, got %+v", got.SessionsByStatus)
	}
	if got.InjectionsTotal != 2 {
		t.Errorf("injections: want 2 in window, got %d", got.InjectionsTotal)
	}
	if len(got.TopSurfaced) != 1 {
		t.Fatalf("want 1 top-surfaced entry, got %d", len(got.TopSurfaced))
	}
	top := got.TopSurfaced[0]
	if top.Title != "fresh correction" || top.Count != 2 {
		t.Errorf("top surfaced: want (fresh correction, 2), got (%s, %d)", top.Title, top.Count)
	}
}

func TestDigestEmptyStore(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	got, err := db.Digest(context.Background(), time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.ObservationsSaved != 0 || got.InjectionsTotal != 0 || got.SessionsOpened != 0 {
		t.Errorf("empty store must produce zero digest, got %+v", got)
	}
}
