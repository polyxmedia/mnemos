package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func openSessDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSessionRecentReturnsAll(t *testing.T) {
	db := openSessDB(t)
	store := db.Sessions()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		s := &session.Session{
			ID: ulid.Make().String(), AgentID: "default",
			Project: "p", Goal: "g", StartedAt: time.Now().UTC(),
		}
		if err := store.Insert(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	list, err := store.Recent(ctx, "default", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Errorf("want 3 sessions, got %d", len(list))
	}
}

func TestSessionRecentFiltersByAgent(t *testing.T) {
	db := openSessDB(t)
	store := db.Sessions()
	ctx := context.Background()

	for _, agent := range []string{"a", "a", "b"} {
		s := &session.Session{
			ID: ulid.Make().String(), AgentID: agent,
			Project: "p", StartedAt: time.Now().UTC(),
		}
		if err := store.Insert(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	aList, _ := store.Recent(ctx, "a", 10)
	if len(aList) != 2 {
		t.Errorf("agent-a scoping failed, got %d", len(aList))
	}
	allList, _ := store.Recent(ctx, "", 10)
	if len(allList) != 3 {
		t.Errorf("unscoped should return all, got %d", len(allList))
	}
}

func TestSessionListOpenScopesAndOrders(t *testing.T) {
	db := openSessDB(t)
	store := db.Sessions()
	ctx := context.Background()

	// Explicit distinct Go timestamps: ListOpen orders by started_at and
	// same-second inserts would make newest-first assertions collide.
	base := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	older := &session.Session{
		ID: ulid.Make().String(), AgentID: "default",
		Project: "p", StartedAt: base,
	}
	newer := &session.Session{
		ID: ulid.Make().String(), AgentID: "default",
		Project: "p", StartedAt: base.Add(time.Second),
	}
	other := &session.Session{
		ID: ulid.Make().String(), AgentID: "default",
		Project: "q", StartedAt: base,
	}
	closed := &session.Session{
		ID: ulid.Make().String(), AgentID: "default",
		Project: "p", StartedAt: base,
	}
	for _, s := range []*session.Session{older, newer, other, closed} {
		if err := store.Insert(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	_ = store.Close(ctx, session.CloseInput{ID: closed.ID, Summary: "done", Status: session.StatusOK})

	open, err := store.ListOpen(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 {
		t.Fatalf("want 2 open sessions in project p, got %d", len(open))
	}
	if open[0].ID != newer.ID {
		t.Errorf("want newest first, got %s", open[0].ID)
	}

	all, err := store.ListOpen(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("unscoped should return all open sessions, got %d", len(all))
	}
}

func TestSessionCurrentNoneWhenAllClosed(t *testing.T) {
	db := openSessDB(t)
	store := db.Sessions()
	ctx := context.Background()

	s := &session.Session{
		ID: ulid.Make().String(), AgentID: "default",
		Project: "p", StartedAt: time.Now().UTC(),
	}
	_ = store.Insert(ctx, s)
	_ = store.Close(ctx, session.CloseInput{ID: s.ID, Summary: "done", Status: session.StatusOK})

	_, err := store.Current(ctx, "default")
	if err == nil {
		t.Error("expected ErrNotFound when all sessions closed")
	}
}
