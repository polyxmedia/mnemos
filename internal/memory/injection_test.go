package memory_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// contextRecorderFake captures Log calls from Service.Context.
type contextRecorderFake struct {
	channel injection.Channel
	agentID string
	project string
	refs    []injection.Ref
	calls   int
}

func (r *contextRecorderFake) Log(_ context.Context, channel injection.Channel, agentID, project, _ string, refs []injection.Ref) error {
	r.channel = channel
	r.agentID = agentID
	r.project = project
	r.refs = append(r.refs, refs...)
	r.calls++
	return nil
}

func TestContextLogsSurfacedObservations(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rec := &contextRecorderFake{}
	svc := memory.NewService(memory.Config{
		Store:      db.Observations(),
		Injections: rec,
	})
	ctx := context.Background()

	saved, err := svc.Save(ctx, memory.SaveInput{
		Title:   "WAL mode for sqlite",
		Content: "enable WAL so readers do not block the writer",
		Type:    memory.TypeDecision,
		Project: "mnemos",
	})
	if err != nil {
		t.Fatal(err)
	}

	block, err := svc.Context(ctx, memory.ContextInput{
		Query:   "sqlite WAL readers",
		AgentID: "default",
		Project: "mnemos",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Observations) == 0 {
		t.Fatal("context block unexpectedly empty")
	}

	if rec.calls != 1 {
		t.Fatalf("want 1 Log call, got %d", rec.calls)
	}
	if rec.channel != injection.ChannelContext {
		t.Errorf("want channel context, got %s", rec.channel)
	}
	if rec.agentID != "default" || rec.project != "mnemos" {
		t.Errorf("input scope lost: agent=%q project=%q", rec.agentID, rec.project)
	}
	found := false
	for _, ref := range rec.refs {
		if ref.Kind == injection.KindObservation && ref.ID == saved.Observation.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("surfaced observation %s missing from logged refs: %+v", saved.Observation.ID, rec.refs)
	}
}

func TestContextWithEmptyResultDoesNotLog(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rec := &contextRecorderFake{}
	svc := memory.NewService(memory.Config{Store: db.Observations(), Injections: rec})

	if _, err := svc.Context(context.Background(), memory.ContextInput{Query: "nothing matches"}); err != nil {
		t.Fatal(err)
	}
	if rec.calls != 0 {
		t.Errorf("empty context must not log, got %d calls", rec.calls)
	}
}
