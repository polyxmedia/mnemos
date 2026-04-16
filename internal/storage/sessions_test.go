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
