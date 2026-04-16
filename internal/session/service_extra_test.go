package session_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func newStoreFixture(t *testing.T) *session.Service {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return session.NewService(session.Config{Store: db.Sessions()})
}

func TestGetByID(t *testing.T) {
	svc := newStoreFixture(t)
	ctx := context.Background()
	s, _ := svc.Open(ctx, session.OpenInput{Project: "p", Goal: "g"})
	got, err := svc.Get(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Goal != "g" {
		t.Errorf("got %+v", got)
	}
}

func TestStatusValid(t *testing.T) {
	for _, s := range []session.Status{
		session.StatusOK, session.StatusFailed, session.StatusBlocked, session.StatusAbandoned,
	} {
		if !s.Valid() {
			t.Errorf("%s should be valid", s)
		}
	}
	if session.Status("bogus").Valid() {
		t.Error("bogus status should not be valid")
	}
}

func TestCloseWithInvalidStatusRejected(t *testing.T) {
	svc := newStoreFixture(t)
	ctx := context.Background()
	s, _ := svc.Open(ctx, session.OpenInput{})
	err := svc.Close(ctx, session.CloseInput{
		ID: s.ID, Summary: "done", Status: session.Status("garbage"),
	})
	if err == nil {
		t.Error("invalid status must be rejected")
	}
}

func TestCloseUnknownSession(t *testing.T) {
	svc := newStoreFixture(t)
	err := svc.Close(context.Background(), session.CloseInput{ID: "nonexistent", Summary: "x"})
	if err == nil {
		t.Error("closing unknown session must error")
	}
}
