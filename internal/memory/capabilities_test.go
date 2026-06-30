package memory_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func hasTier(tiers []string, want string) bool {
	for _, t := range tiers {
		if t == want {
			return true
		}
	}
	return false
}

func TestCapabilitiesAlwaysReportsCoreAndProvenance(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	t.Cleanup(func() { _ = db.Close() })

	svc := memory.NewService(memory.Config{Store: db.Observations()}) // no embedder
	c := svc.Capabilities()

	if c.Spec != memory.SpecVersion {
		t.Errorf("spec = %q, want %q", c.Spec, memory.SpecVersion)
	}
	if !c.Bitemporal || !c.Provenance || !c.TrustTiers || !c.TypedLinks || !c.DeterministicDedup {
		t.Errorf("schema-level capabilities must all be true, got %+v", c)
	}
	if !hasTier(c.Tiers, "memory-core") || !hasTier(c.Tiers, "provenance") {
		t.Errorf("tiers must include memory-core and provenance, got %v", c.Tiers)
	}
}

// TestCapabilitiesHybridTierIsBehaviorBound is the anti-lie guard: the
// embeddings tier must track whether hybrid retrieval can actually run, not a
// static config or constant.
func TestCapabilitiesHybridTierIsBehaviorBound(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	t.Cleanup(func() { _ = db.Close() })

	// No embedder: hybrid is not live, so the embeddings tier must be absent.
	off := memory.NewService(memory.Config{Store: db.Observations()})
	if c := off.Capabilities(); c.HybridRetrieval || hasTier(c.Tiers, "embeddings") {
		t.Errorf("without embedder the embeddings tier must be absent, got tiers=%v hybrid=%v", c.Tiers, c.HybridRetrieval)
	}

	// Embedder configured: hybrid is live, so the embeddings tier must appear.
	on := memory.NewService(memory.Config{
		Store:    db.Observations(),
		Embedder: fixedEmbedder{dim: 4, vec: []float32{1, 0, 0, 0}},
	})
	c := on.Capabilities()
	if !c.HybridRetrieval || !hasTier(c.Tiers, "embeddings") {
		t.Errorf("with embedder the embeddings tier must be present, got tiers=%v hybrid=%v", c.Tiers, c.HybridRetrieval)
	}

	// Federation is unbuilt regardless of embedder state.
	if c.Federation || hasTier(c.Tiers, "federation") {
		t.Errorf("federation must be absent until Bet 2 phase 4, got tiers=%v", c.Tiers)
	}
}
