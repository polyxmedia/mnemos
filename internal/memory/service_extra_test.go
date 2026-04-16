package memory_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func newSvcWithEmbedder(t *testing.T, e memory.Embedder) *memory.Service {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return memory.NewService(memory.Config{
		Store:    db.Observations(),
		Embedder: e,
	})
}

// stubEmbedder returns a deterministic vector based on the first byte.
type stubEmbedder struct{ dim int }

func (s stubEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, s.dim)
	if text == "" {
		return nil, nil
	}
	v[0] = float32(int(text[0]))
	return v, nil
}
func (s stubEmbedder) Dimension() int { return s.dim }
func (s stubEmbedder) Model() string  { return "stub" }

func TestHybridEnabledWithEmbedder(t *testing.T) {
	svc := newSvcWithEmbedder(t, stubEmbedder{dim: 4})
	if !svc.HybridEnabled() {
		t.Error("hybrid should be enabled with a non-zero-dim embedder")
	}
}

func TestHybridDisabledWithNoop(t *testing.T) {
	svc := newSvcWithEmbedder(t, stubEmbedder{dim: 0})
	if svc.HybridEnabled() {
		t.Error("hybrid should be disabled when embedder dimension is 0")
	}
}

func TestSaveWithEmbedderAttachesVector(t *testing.T) {
	ctx := context.Background()
	svc := newSvcWithEmbedder(t, stubEmbedder{dim: 4})

	res, err := svc.Save(ctx, memory.SaveInput{
		Title: "seed", Content: "c", Type: memory.TypePattern,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, res.Observation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Embedding) != 4 {
		t.Errorf("expected 4-dim embedding, got %d", len(got.Embedding))
	}
	if !strings.HasPrefix(got.EmbeddingModel, "stub") {
		t.Errorf("wrong model id: %s", got.EmbeddingModel)
	}
}

func TestInvalidateHidesFromDefaultSearch(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	res, _ := svc.Save(ctx, memory.SaveInput{
		Title: "deploy fridays", Content: "we deploy fridays",
		Type: memory.TypePreference,
	})
	if err := svc.Invalidate(ctx, res.Observation.ID); err != nil {
		t.Fatal(err)
	}
	hits, err := svc.Search(ctx, memory.SearchInput{Query: "fridays"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("invalidated observation should not appear in default search")
	}
	// Stale mode should surface it.
	hits, err = svc.Search(ctx, memory.SearchInput{Query: "fridays", IncludeStale: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Errorf("IncludeStale should return invalidated, got %d", len(hits))
	}
}

func TestLinkValidatesType(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()
	a, _ := svc.Save(ctx, memory.SaveInput{
		Title: "a", Content: "a", Type: memory.TypeContext,
	})
	b, _ := svc.Save(ctx, memory.SaveInput{
		Title: "b", Content: "b", Type: memory.TypeContext,
	})
	if err := svc.Link(ctx, a.Observation.ID, b.Observation.ID, "bogus"); err == nil {
		t.Error("expected error on bogus link type")
	}
}
