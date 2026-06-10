package prewarm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func newPremortemFixture(t *testing.T) (*prewarm.Service, *memory.Service, *session.Service, *recorderFake) {
	t.Helper()
	db, err := storage.Open(context.Background(), t.TempDir()+"/m.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rec := &recorderFake{}
	mem := memory.NewService(memory.Config{Store: db.Observations()})
	sess := session.NewService(session.Config{Store: db.Sessions()})
	pw := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(),
		Sessions:     db.Sessions(),
		Skills:       db.Skills(),
		Touches:      db.Touches(),
		Injections:   rec,
	})
	return pw, mem, sess, rec
}

func TestPremortemSurfacesCorrectionsAndFailedSessions(t *testing.T) {
	pw, mem, sess, rec := newPremortemFixture(t)
	ctx := context.Background()

	saved, err := mem.Save(ctx, memory.SaveInput{
		Title:   "oauth retry on 401 is wrong",
		Content: "tried retrying on 401; wrong because 401 is auth failure not transient; fix: refresh token then retry once",
		Type:    memory.TypeCorrection,
		Project: "api",
	})
	if err != nil {
		t.Fatal(err)
	}

	failed, err := sess.Open(ctx, session.OpenInput{Project: "api", Goal: "implement oauth token retry flow"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(ctx, session.CloseInput{
		ID: failed.ID, Summary: "blocked on refresh token race", Status: session.StatusFailed,
	}); err != nil {
		t.Fatal(err)
	}
	// An ok session with the same goal shape must NOT appear.
	okSess, _ := sess.Open(ctx, session.OpenInput{Project: "api", Goal: "oauth retry cleanup"})
	_ = sess.Close(ctx, session.CloseInput{ID: okSess.ID, Summary: "fine", Status: session.StatusOK})

	block, err := pw.Build(ctx, prewarm.Request{
		Mode:    prewarm.ModePremortem,
		Project: "api",
		Goal:    "add oauth retry handling to the api client",
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(block.Text, "how similar attempts failed") {
		t.Errorf("missing corrections section:\n%s", block.Text)
	}
	if !strings.Contains(block.Text, "oauth retry on 401 is wrong") {
		t.Errorf("correction not surfaced:\n%s", block.Text)
	}
	if !strings.Contains(block.Text, "similar sessions that failed") {
		t.Errorf("missing failed-sessions section:\n%s", block.Text)
	}
	if !strings.Contains(block.Text, "blocked on refresh token race") {
		t.Errorf("failed session not surfaced:\n%s", block.Text)
	}
	if strings.Contains(block.Text, "oauth retry cleanup") {
		t.Errorf("ok session must not appear in a premortem:\n%s", block.Text)
	}

	if rec.channel != injection.ChannelPremortem {
		t.Errorf("premortem mode must log channel premortem, got %s", rec.channel)
	}
	found := false
	for _, ref := range block.Surfaced {
		if ref.ID == saved.Observation.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("surfaced refs missing the correction: %+v", block.Surfaced)
	}
}

func TestPremortemEmptyStoreYieldsEmptyBlock(t *testing.T) {
	pw, _, _, rec := newPremortemFixture(t)

	block, err := pw.Build(context.Background(), prewarm.Request{
		Mode: prewarm.ModePremortem,
		Goal: "anything at all",
	})
	if err != nil {
		t.Fatal(err)
	}
	if block.Text != "" {
		t.Errorf("empty store should yield empty premortem, got: %q", block.Text)
	}
	if rec.calls != 0 {
		t.Errorf("nothing surfaced means nothing logged, got %d calls", rec.calls)
	}
}
