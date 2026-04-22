package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func openLinkDB(t *testing.T) (*storage.DB, *memory.Service) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, memory.NewService(memory.Config{Store: db.Observations()})
}

func TestListLinksReturnsOnlyLiveEndpoints(t *testing.T) {
	db, mem := openLinkDB(t)
	ctx := context.Background()

	// Seed: three observations.
	a, _ := mem.Save(ctx, memory.SaveInput{Title: "use sqlite", Content: "x", Type: memory.TypeConvention, Project: "p"})
	b, _ := mem.Save(ctx, memory.SaveInput{Title: "actually pg is better", Content: "y", Type: memory.TypeCorrection, Project: "p"})
	c, _ := mem.Save(ctx, memory.SaveInput{Title: "dead challenger", Content: "z", Type: memory.TypeCorrection, Project: "p"})

	store := db.Observations()
	// Two contradicts edges both targeting A.
	if err := store.Link(ctx, b.Observation.ID, a.Observation.ID, memory.LinkContradicts); err != nil {
		t.Fatal(err)
	}
	if err := store.Link(ctx, c.Observation.ID, a.Observation.ID, memory.LinkContradicts); err != nil {
		t.Fatal(err)
	}
	// And one unrelated edge type — must not show up.
	if err := store.Link(ctx, b.Observation.ID, a.Observation.ID, memory.LinkRefines); err != nil {
		t.Fatal(err)
	}

	edges, err := store.ListLinks(ctx, memory.LinkContradicts, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Fatalf("want 2 contradicts edges, got %d", len(edges))
	}
	for _, e := range edges {
		if e.TargetID != a.Observation.ID {
			t.Errorf("edge target = %s, want %s", e.TargetID, a.Observation.ID)
		}
		if e.LinkType != memory.LinkContradicts {
			t.Errorf("edge type = %s", e.LinkType)
		}
		if e.SourceTitle == "" || e.TargetTitle == "" {
			t.Errorf("titles should be populated: %+v", e)
		}
	}

	// Invalidate the challenger c — its edge should drop out of the
	// next ListLinks call. Exercises the "only live endpoints" clause.
	if err := store.Invalidate(ctx, c.Observation.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	edges, err = store.ListLinks(ctx, memory.LinkContradicts, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("invalidated source should drop edge, got %d", len(edges))
	}
	if edges[0].SourceID != b.Observation.ID {
		t.Errorf("survivor should be b, got %s", edges[0].SourceID)
	}
}

func TestListLinksAgentFilter(t *testing.T) {
	db, mem := openLinkDB(t)
	ctx := context.Background()

	aA, _ := mem.Save(ctx, memory.SaveInput{Title: "agent a target", Content: "x", Type: memory.TypeConvention, AgentID: "alpha", Project: "p"})
	aB, _ := mem.Save(ctx, memory.SaveInput{Title: "agent a source", Content: "y", Type: memory.TypeCorrection, AgentID: "alpha", Project: "p"})
	bA, _ := mem.Save(ctx, memory.SaveInput{Title: "agent b target", Content: "x", Type: memory.TypeConvention, AgentID: "beta", Project: "p"})
	bB, _ := mem.Save(ctx, memory.SaveInput{Title: "agent b source", Content: "y", Type: memory.TypeCorrection, AgentID: "beta", Project: "p"})

	store := db.Observations()
	_ = store.Link(ctx, aB.Observation.ID, aA.Observation.ID, memory.LinkContradicts)
	_ = store.Link(ctx, bB.Observation.ID, bA.Observation.ID, memory.LinkContradicts)

	alpha, err := store.ListLinks(ctx, memory.LinkContradicts, "alpha", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha) != 1 || alpha[0].TargetAgent != "alpha" {
		t.Errorf("alpha filter should return only alpha-owned targets: %+v", alpha)
	}
	all, _ := store.ListLinks(ctx, memory.LinkContradicts, "", 100)
	if len(all) != 2 {
		t.Errorf("empty agent filter should return all, got %d", len(all))
	}
}
