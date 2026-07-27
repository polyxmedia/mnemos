package session_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// newClockedService builds a session service whose clock the test drives,
// so started_at spacing is exact. Two time.Now() calls can land on the
// same tick on a coarse clock, which would make the twin-window boundary
// assertions below flap.
func newClockedService(t *testing.T, now *time.Time) *session.Service {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return session.NewService(session.Config{
		Store: db.Sessions(),
		Clock: func() time.Time { return *now },
	})
}

func TestCloseAllOpenClosesTwinsButSparesLiveSibling(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	svc := newClockedService(t, &now)

	// A sibling terminal opened this session hours ago and is still working.
	sibling, err := svc.Open(ctx, session.OpenInput{Project: "taken", Goal: "long running"})
	if err != nil {
		t.Fatal(err)
	}

	// A second terminal launches much later. Its user-scope and
	// project-scope SessionStart hooks each open a session milliseconds
	// apart — these two are twins and must close together.
	now = now.Add(6 * time.Hour)
	if _, err := svc.Open(ctx, session.OpenInput{Project: "taken", Goal: "new terminal"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(200 * time.Millisecond)
	if _, err := svc.Open(ctx, session.OpenInput{Project: "taken", Goal: "new terminal"}); err != nil {
		t.Fatal(err)
	}

	closed, err := svc.CloseAllOpen(ctx, "taken", session.CloseInput{
		Summary: "terminal exited", Status: session.StatusOK,
	})
	if err != nil {
		t.Fatalf("close all open: %v", err)
	}
	if closed != 2 {
		t.Errorf("want the twin pair closed, got %d", closed)
	}

	open, err := svc.ListOpen(ctx, "taken")
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("want the live sibling still open, got %d open sessions", len(open))
	}
	if open[0].ID != sibling.ID {
		t.Errorf("wrong session survived: want sibling %s, got %s", sibling.ID, open[0].ID)
	}
}

func TestCloseAllOpenOnEmptyProjectIsNoop(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	svc := newClockedService(t, &now)

	closed, err := svc.CloseAllOpen(ctx, "nothing-here", session.CloseInput{Status: session.StatusOK})
	if err != nil {
		t.Fatalf("want a clean no-op, got %v", err)
	}
	if closed != 0 {
		t.Errorf("want 0 closed, got %d", closed)
	}
}
