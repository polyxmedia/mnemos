package prewarm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// recorderFake captures Log calls so tests can assert what Build surfaced
// without a real injection store.
type recorderFake struct {
	channel injection.Channel
	project string
	session string
	refs    []injection.Ref
	calls   int
}

func (r *recorderFake) Log(_ context.Context, channel injection.Channel, _, project, sessionID string, refs []injection.Ref) error {
	r.channel = channel
	r.project = project
	r.session = sessionID
	r.refs = append(r.refs, refs...)
	r.calls++
	return nil
}

func newInjectionFixture(t *testing.T) (*prewarm.Service, *memory.Service, *skills.Service, *recorderFake) {
	t.Helper()
	db, err := storage.Open(context.Background(), t.TempDir()+"/m.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rec := &recorderFake{}
	mem := memory.NewService(memory.Config{Store: db.Observations()})
	skl := skills.NewService(skills.Config{Store: db.Skills()})
	pw := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(),
		Sessions:     db.Sessions(),
		Skills:       db.Skills(),
		Touches:      db.Touches(),
		Injections:   rec,
	})
	return pw, mem, skl, rec
}

func TestBuildSurfacesAndLogsObservationRefs(t *testing.T) {
	pw, mem, _, rec := newInjectionFixture(t)
	ctx := context.Background()

	saved, err := mem.Save(ctx, memory.SaveInput{
		Title:   "use %w for errors",
		Content: "all errors wrapped with fmt.Errorf",
		Type:    memory.TypeConvention,
		Project: "mnemos",
	})
	if err != nil {
		t.Fatal(err)
	}

	block, err := pw.Build(ctx, prewarm.Request{
		Mode:      prewarm.ModeSessionStart,
		Project:   "mnemos",
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, ref := range block.Surfaced {
		if ref.Kind == injection.KindObservation && ref.ID == saved.Observation.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("convention %s missing from block.Surfaced: %+v", saved.Observation.ID, block.Surfaced)
	}

	if rec.calls != 1 {
		t.Fatalf("want 1 Log call, got %d", rec.calls)
	}
	if rec.channel != injection.ChannelPrewarm {
		t.Errorf("session_start mode must log channel prewarm, got %s", rec.channel)
	}
	if rec.project != "mnemos" || rec.session != "sess-1" {
		t.Errorf("request scope lost: project=%q session=%q", rec.project, rec.session)
	}
	if len(rec.refs) != len(block.Surfaced) {
		t.Errorf("logged refs (%d) must match surfaced refs (%d)", len(rec.refs), len(block.Surfaced))
	}
}

func TestBuildSurfacesSkillRefs(t *testing.T) {
	pw, _, skl, rec := newInjectionFixture(t)
	ctx := context.Background()

	created, err := skl.Save(ctx, skills.SaveInput{
		Name:        "migration-pattern",
		Description: "add a numbered sql migration file",
		Procedure:   "create NNNN_name.sql under internal/storage/migrations",
		Tags:        []string{"migration", "schema"},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = pw.Build(ctx, prewarm.Request{
		Mode:    prewarm.ModeSessionStart,
		Project: "mnemos",
		Goal:    "add a schema migration",
	})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, ref := range rec.refs {
		if ref.Kind == injection.KindSkill && ref.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("matched skill %s missing from logged refs: %+v", created.ID, rec.refs)
	}
}

func TestBuildRecoveryModeLogsRecoveryChannel(t *testing.T) {
	pw, mem, _, rec := newInjectionFixture(t)
	ctx := context.Background()

	if _, err := mem.Save(ctx, memory.SaveInput{
		Title: "conv", Content: "body", Type: memory.TypeConvention, Project: "mnemos",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := pw.Build(ctx, prewarm.Request{
		Mode:    prewarm.ModeCompactionRecovery,
		Project: "mnemos",
	}); err != nil {
		t.Fatal(err)
	}
	if rec.calls == 0 {
		t.Fatal("recovery build with a convention must log surfacings")
	}
	if rec.channel != injection.ChannelRecovery {
		t.Errorf("recovery mode must log channel recovery, got %s", rec.channel)
	}
}

func TestBuildWithoutRecorderStillReturnsSurfaced(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir()+"/m.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mem := memory.NewService(memory.Config{Store: db.Observations()})
	pw := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(),
		Sessions:     db.Sessions(),
		Skills:       db.Skills(),
		Touches:      db.Touches(),
		// Injections deliberately nil.
	})
	ctx := context.Background()
	if _, err := mem.Save(ctx, memory.SaveInput{
		Title: "conv", Content: "body", Type: memory.TypeConvention, Project: "mnemos",
	}); err != nil {
		t.Fatal(err)
	}

	block, err := pw.Build(ctx, prewarm.Request{Mode: prewarm.ModeSessionStart, Project: "mnemos"})
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Surfaced) == 0 {
		t.Error("Surfaced must be populated even when no recorder is wired")
	}
	if !strings.Contains(block.Text, "conv") {
		t.Errorf("block text missing convention: %q", block.Text)
	}
}
