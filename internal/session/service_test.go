package session_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func newService(t *testing.T) *session.Service {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return session.NewService(session.Config{Store: db.Sessions()})
}

func TestOpenCloseCurrent(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()

	s, err := svc.Open(ctx, session.OpenInput{Project: "mnemos", Goal: "ship storage"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := svc.Current(ctx, "default")
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if current.ID != s.ID {
		t.Errorf("current returned wrong session")
	}

	if err := svc.Close(ctx, session.CloseInput{
		ID:         s.ID,
		Summary:    "done",
		Reflection: "bi-temporal upfront saved pain later",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Current(ctx, "default"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("expected ErrNotFound after close, got %v", err)
	}
}

func TestCloseRejectsEmptyID(t *testing.T) {
	svc := newService(t)
	if err := svc.Close(context.Background(), session.CloseInput{}); err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestRecentOrderedDescending(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	ids := []string{}
	for i := 0; i < 3; i++ {
		s, err := svc.Open(ctx, session.OpenInput{Goal: "work"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID)
		// Windows' low-resolution clock (15 ms on some systems) can give
		// identical timestamps across rapid inserts, which breaks
		// started_at DESC ordering. Session IDs are ULIDs which sort
		// correctly even at the same ms — so we sleep just enough to
		// guarantee distinct started_at values too.
		time.Sleep(time.Millisecond * 20)
	}
	got, err := svc.Recent(ctx, "default", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 sessions, got %d", len(got))
	}
	if got[0].ID != ids[2] {
		t.Errorf("expected most recent first, got %s (ids: %v)", got[0].ID, ids)
	}
}
