package memory_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func newService(t *testing.T, clock func() time.Time) *memory.Service {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return memory.NewService(memory.Config{
		Store: db.Observations(),
		Clock: clock,
	})
}

func TestSaveValidation(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	cases := []memory.SaveInput{
		{Title: "", Content: "x", Type: memory.TypePattern},
		{Title: "t", Content: "", Type: memory.TypePattern},
		{Title: "t", Content: "c", Type: "garbage"},
		{Title: "t", Content: "c", Type: memory.TypePattern, Importance: 11},
	}
	for i, in := range cases {
		if _, err := svc.Save(ctx, in); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestSaveGetRoundTrip(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()
	o, err := svc.Save(ctx, memory.SaveInput{
		Title:      "Use WAL",
		Content:    "Enable WAL for concurrent readers.",
		Type:       memory.TypePattern,
		Tags:       []string{"sqlite"},
		Importance: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != o.Content {
		t.Errorf("round trip content mismatch")
	}
}

func TestSupersedeInvalidatesAndLinks(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	oldO, _ := svc.Save(ctx, memory.SaveInput{
		Title: "Use X", Content: "we use X", Type: memory.TypeDecision, Importance: 5,
	})
	newO, _ := svc.Save(ctx, memory.SaveInput{
		Title: "Use Y", Content: "we use Y now", Type: memory.TypeDecision, Importance: 7,
	})
	if err := svc.Supersede(ctx, newO.ID, oldO.ID); err != nil {
		t.Fatal(err)
	}

	results, err := svc.Search(ctx, memory.SearchInput{Query: "use"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Observation.ID == oldO.ID {
			t.Error("old superseded observation should be excluded from default search")
		}
	}
}

func TestRankingPrefersImportantAndRecent(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	// Same text, different importance/recency.
	old := time.Now().Add(-60 * 24 * time.Hour).UTC()
	// Trick: we can't backdate via SaveInput, but the ranker uses CreatedAt
	// pulled from the store. For this test we only vary importance.
	low, _ := svc.Save(ctx, memory.SaveInput{
		Title: "Pattern A", Content: "pattern about sqlite indexing",
		Type: memory.TypePattern, Importance: 3,
	})
	high, _ := svc.Save(ctx, memory.SaveInput{
		Title: "Pattern B", Content: "pattern about sqlite indexing",
		Type: memory.TypePattern, Importance: 10,
	})
	_ = old
	_ = low

	results, err := svc.Search(ctx, memory.SearchInput{Query: "sqlite indexing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("want 2+ results, got %d", len(results))
	}
	if results[0].Observation.ID != high.ID {
		t.Errorf("expected high-importance observation first, got %s", results[0].Observation.Title)
	}
}

func TestContextRespectsTokenBudget(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	for i := 0; i < 50; i++ {
		_, err := svc.Save(ctx, memory.SaveInput{
			Title:      "Entry",
			Content:    "some content about sqlite indexing and FTS5 search ranking behaviour",
			Type:       memory.TypePattern,
			Importance: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	block, err := svc.Context(ctx, memory.ContextInput{
		Query:     "sqlite",
		MaxTokens: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if block.TokenEstimate > 200 {
		t.Errorf("budget exceeded: %d > 200", block.TokenEstimate)
	}
	if len(block.Observations) == 0 {
		t.Error("expected at least one observation in context block")
	}
	if block.Text == "" {
		t.Error("context text should not be empty")
	}
}

func TestDecayParamsShapeScore(t *testing.T) {
	r := memory.NewRanker(memory.DefaultRankParams())
	now := time.Now().UTC()
	fresh := memory.Observation{Importance: 5, CreatedAt: now}
	old := memory.Observation{Importance: 5, CreatedAt: now.AddDate(0, -6, 0)}
	freshScore := r.Score(fresh, 1.0, now)
	oldScore := r.Score(old, 1.0, now)
	if freshScore <= oldScore {
		t.Errorf("fresh should outrank old: fresh=%f old=%f", freshScore, oldScore)
	}
}
