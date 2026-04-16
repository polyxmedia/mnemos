package memory_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// ranker that returns a specific vector so we can reason about cosine ranking.
type fixedEmbedder struct {
	dim int
	vec []float32
	err error
}

func (f fixedEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.vec, nil
}
func (f fixedEmbedder) Dimension() int { return f.dim }
func (f fixedEmbedder) Model() string  { return "fixed" }

func TestHybridSearchExercisesFuseWithVectors(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	t.Cleanup(func() { _ = db.Close() })

	svc := memory.NewService(memory.Config{
		Store:    db.Observations(),
		Embedder: fixedEmbedder{dim: 4, vec: []float32{1, 0, 0, 0}},
	})
	ctx := context.Background()

	for i, text := range []string{
		"sqlite indexing patterns",
		"sqlite performance tips",
		"go generics explained",
	} {
		_, err := svc.Save(ctx, memory.SaveInput{
			Title: "entry", Content: text, Type: memory.TypePattern,
			Importance: 5 + i,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Hybrid search fires fuseWithVectors because HybridEnabled() is true.
	results, err := svc.Search(ctx, memory.SearchInput{Query: "sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Errorf("want at least 2 hits, got %d", len(results))
	}
}

func TestSearchFailsOpenOnEmbedError(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	t.Cleanup(func() { _ = db.Close() })

	svc := memory.NewService(memory.Config{
		Store:    db.Observations(),
		Embedder: fixedEmbedder{dim: 4, err: errFake},
	})
	ctx := context.Background()
	_, _ = svc.Save(ctx, memory.SaveInput{
		Title: "x", Content: "sqlite", Type: memory.TypePattern,
	})
	// Search should still work even if embedding the query errors — we
	// fall open to BM25-only.
	results, err := svc.Search(ctx, memory.SearchInput{Query: "sqlite"})
	if err != nil {
		t.Fatalf("search must not fail on embed error: %v", err)
	}
	if len(results) == 0 {
		t.Error("BM25 fallback should still return results")
	}
}

func TestServiceStatsDelegatesToStore(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	t.Cleanup(func() { _ = db.Close() })

	svc := memory.NewService(memory.Config{Store: db.Observations()})
	_, err := svc.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

var errFake = &fakeErr{msg: "embed kaput"}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }
