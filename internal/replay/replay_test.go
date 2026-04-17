package replay_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/replay"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func newFixture(t *testing.T) (*replay.Service, *memory.Service, *session.Service, *skills.Service, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mem := memory.NewService(memory.Config{Store: db.Observations()})
	sess := session.NewService(session.Config{Store: db.Sessions()})
	skl := skills.NewService(skills.Config{Store: db.Skills()})
	rp := replay.NewService(replay.Config{
		Observations: db.Observations(),
		Sessions:     db.Sessions(),
		Skills:       db.Skills(),
	})
	return rp, mem, sess, skl, db
}

func TestReplayRejectsMissingSessionID(t *testing.T) {
	rp, _, _, _, _ := newFixture(t)
	if _, err := rp.Build(context.Background(), replay.Request{}); err == nil {
		t.Error("expected error on empty session_id")
	}
}

func TestReplayRejectsUnknownSession(t *testing.T) {
	rp, _, _, _, _ := newFixture(t)
	_, err := rp.Build(context.Background(), replay.Request{SessionID: "nonexistent"})
	if err == nil {
		t.Error("expected error on unknown session")
	}
}

func TestReplayIncludesOriginalObservations(t *testing.T) {
	rp, mem, sess, _, _ := newFixture(t)
	ctx := context.Background()

	s, _ := sess.Open(ctx, session.OpenInput{Project: "proj", Goal: "fix the login bug"})
	_, _ = mem.Save(ctx, memory.SaveInput{
		SessionID: s.ID, Project: "proj",
		Title: "saw nil deref", Content: "handler crashed on nil user",
		Type: memory.TypeBugfix,
	})
	_ = sess.Close(ctx, session.CloseInput{
		ID: s.ID, Summary: "fixed", Status: session.StatusOK,
	})

	block, err := rp.Build(ctx, replay.Request{SessionID: s.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block.Text, "fix the login bug") {
		t.Errorf("replay missing session goal: %s", block.Text)
	}
	if !strings.Contains(block.Text, "saw nil deref") {
		t.Errorf("replay missing original observation: %s", block.Text)
	}
	if len(block.Observations) != 1 {
		t.Errorf("want 1 observation, got %d", len(block.Observations))
	}
}

func TestReplaySurfacesCorrectionsAddedAfter(t *testing.T) {
	rp, mem, sess, _, _ := newFixture(t)
	ctx := context.Background()

	s, _ := sess.Open(ctx, session.OpenInput{Project: "proj", Goal: "oauth retry"})
	_ = sess.Close(ctx, session.CloseInput{ID: s.ID, Summary: "done", Status: session.StatusOK})

	// Sleep so the correction's created_at is strictly after session end.
	time.Sleep(25 * time.Millisecond)

	_, _ = mem.Save(ctx, memory.SaveInput{
		Title:   "oauth retry was wrong",
		Content: "**Tried:** retry on 401\n**Wrong because:** 401 is auth not transient\n**Fix:** refresh then retry",
		Type:    memory.TypeCorrection, Project: "proj", Importance: 8,
	})

	block, err := rp.Build(ctx, replay.Request{SessionID: s.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block.Text, "Corrections recorded since") {
		t.Errorf("expected corrections section, got: %s", block.Text)
	}
	if !strings.Contains(block.Text, "oauth retry was wrong") {
		t.Errorf("correction not surfaced: %s", block.Text)
	}
}

func TestReplaySurfacesConventionsAddedAfter(t *testing.T) {
	rp, mem, sess, _, _ := newFixture(t)
	ctx := context.Background()

	s, _ := sess.Open(ctx, session.OpenInput{Project: "proj", Goal: "anything"})
	_ = sess.Close(ctx, session.CloseInput{ID: s.ID, Summary: "done"})

	time.Sleep(25 * time.Millisecond)
	_, _ = mem.Save(ctx, memory.SaveInput{
		Title: "error wrapping", Content: "use fmt.Errorf with %w everywhere",
		Type: memory.TypeConvention, Project: "proj",
	})

	block, err := rp.Build(ctx, replay.Request{SessionID: s.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block.Text, "Conventions added since") {
		t.Errorf("expected conventions section: %s", block.Text)
	}
}

func TestReplaySurfacesNewSkills(t *testing.T) {
	rp, _, sess, skl, _ := newFixture(t)
	ctx := context.Background()

	s, _ := sess.Open(ctx, session.OpenInput{Project: "proj"})
	_ = sess.Close(ctx, session.CloseInput{ID: s.ID, Summary: "x"})

	time.Sleep(25 * time.Millisecond)
	_, _ = skl.Save(ctx, skills.SaveInput{
		Name:        "oauth-retry-properly",
		Description: "refresh then retry once",
		Procedure:   "1. refresh\n2. retry",
	})

	block, err := rp.Build(ctx, replay.Request{SessionID: s.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block.Text, "Skills learned since") {
		t.Errorf("expected skills section: %s", block.Text)
	}
}

func TestReplayFlagsSupersededObservations(t *testing.T) {
	rp, mem, sess, _, _ := newFixture(t)
	ctx := context.Background()

	s, _ := sess.Open(ctx, session.OpenInput{Project: "proj", Goal: "decision log"})
	oldRes, _ := mem.Save(ctx, memory.SaveInput{
		SessionID: s.ID, Project: "proj",
		Title: "use REST", Content: "we use REST for X",
		Type: memory.TypeDecision,
	})
	_ = sess.Close(ctx, session.CloseInput{ID: s.ID, Summary: "done"})

	time.Sleep(25 * time.Millisecond)
	newRes, _ := mem.Save(ctx, memory.SaveInput{
		Title: "use gRPC", Content: "we now use gRPC for X",
		Type: memory.TypeDecision, Project: "proj",
	})
	_ = mem.Supersede(ctx, newRes.Observation.ID, oldRes.Observation.ID)

	block, err := rp.Build(ctx, replay.Request{SessionID: s.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block.Text, "no longer true") {
		t.Errorf("expected superseded section: %s", block.Text)
	}
	if !strings.Contains(block.Text, "~~use REST~~") {
		t.Errorf("expected strikethrough for old fact: %s", block.Text)
	}
	if len(block.Superseded) != 1 {
		t.Errorf("want 1 superseded, got %d", len(block.Superseded))
	}
}

func TestReplayRespectsTokenBudget(t *testing.T) {
	rp, mem, sess, _, _ := newFixture(t)
	ctx := context.Background()

	s, _ := sess.Open(ctx, session.OpenInput{Project: "proj", Goal: "huge session"})
	for i := 0; i < 20; i++ {
		_, _ = mem.Save(ctx, memory.SaveInput{
			SessionID: s.ID, Project: "proj",
			Title:   "observation",
			Content: strings.Repeat("x ", 500),
			Type:    memory.TypeContext,
		})
	}
	_ = sess.Close(ctx, session.CloseInput{ID: s.ID, Summary: "x"})

	block, err := rp.Build(ctx, replay.Request{
		SessionID: s.ID, MaxTokens: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if block.TokenEstimate > 600 {
		t.Errorf("budget exceeded: %d > 500", block.TokenEstimate)
	}
}
