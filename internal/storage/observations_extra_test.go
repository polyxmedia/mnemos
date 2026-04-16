package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func openObsDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seed(t *testing.T, store memory.Store, title, content string, typ memory.ObsType) *memory.Observation {
	t.Helper()
	now := time.Now().UTC()
	o := &memory.Observation{
		ID: ulid.Make().String(), AgentID: "default", Project: "proj",
		Title: title, Content: content, Type: typ, Importance: 5,
		CreatedAt: now, ValidFrom: now, ContentHash: title + ":" + content,
	}
	if err := store.Insert(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestFindByContentHashHit(t *testing.T) {
	db := openObsDB(t)
	store := db.Observations()
	ctx := context.Background()

	o := seed(t, store, "t", "c", memory.TypePattern)
	got, err := store.FindByContentHash(ctx, "default", "proj", o.ContentHash)
	if err != nil || got == nil {
		t.Fatalf("expected hit, got %v err=%v", got, err)
	}
	if got.ID != o.ID {
		t.Errorf("wrong ID")
	}
}

func TestFindByContentHashMiss(t *testing.T) {
	db := openObsDB(t)
	got, err := db.Observations().FindByContentHash(context.Background(),
		"default", "proj", "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestFindByContentHashEmptyHashShortCircuits(t *testing.T) {
	db := openObsDB(t)
	got, err := db.Observations().FindByContentHash(context.Background(),
		"default", "proj", "")
	if err != nil || got != nil {
		t.Errorf("empty hash must short-circuit to (nil, nil), got %v %v", got, err)
	}
}

func TestListByProjectFilters(t *testing.T) {
	db := openObsDB(t)
	store := db.Observations()
	ctx := context.Background()

	_ = seed(t, store, "d1", "decision one", memory.TypeDecision)
	_ = seed(t, store, "p1", "pattern one", memory.TypePattern)
	_ = seed(t, store, "p2", "pattern two", memory.TypePattern)

	all, err := store.ListByProject(ctx, "default", "proj", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}
	patterns, _ := store.ListByProject(ctx, "default", "proj", memory.TypePattern, 100)
	if len(patterns) != 2 {
		t.Errorf("expected 2 patterns, got %d", len(patterns))
	}
}

func TestUpdateEmbedding(t *testing.T) {
	db := openObsDB(t)
	store := db.Observations()
	ctx := context.Background()
	o := seed(t, store, "e", "embedding target", memory.TypePattern)

	vec := []float32{0.1, 0.2, 0.3, 0.4}
	if err := store.UpdateEmbedding(ctx, o.ID, "test-model", vec); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(ctx, o.ID)
	if len(got.Embedding) != 4 || got.EmbeddingModel != "test-model" {
		t.Errorf("embedding not persisted: len=%d model=%q", len(got.Embedding), got.EmbeddingModel)
	}
}

func TestUpdateEmbeddingMissingRowReturnsNotFound(t *testing.T) {
	db := openObsDB(t)
	err := db.Observations().UpdateEmbedding(context.Background(),
		"nonexistent", "m", []float32{1, 2})
	if err == nil {
		t.Error("expected error for missing row")
	}
}

func TestListMissingEmbeddings(t *testing.T) {
	db := openObsDB(t)
	store := db.Observations()
	ctx := context.Background()

	o1 := seed(t, store, "no-vec-1", "x", memory.TypePattern)
	o2 := seed(t, store, "no-vec-2", "y", memory.TypePattern)
	_ = o1
	_ = o2

	missing, err := store.ListMissingEmbeddings(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 2 {
		t.Errorf("expected 2 missing, got %d", len(missing))
	}

	_ = store.UpdateEmbedding(ctx, o1.ID, "m", []float32{1, 2})
	missing2, _ := store.ListMissingEmbeddings(ctx, 100)
	if len(missing2) != 1 {
		t.Errorf("expected 1 missing after update, got %d", len(missing2))
	}
}

func TestMarkExported(t *testing.T) {
	db := openObsDB(t)
	store := db.Observations()
	ctx := context.Background()
	o := seed(t, store, "exp", "content", memory.TypePattern)

	now := time.Now().UTC()
	if err := store.MarkExported(ctx, o.ID, now); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(ctx, o.ID)
	if got.LastExportedAt == nil {
		t.Error("last_exported_at not set")
	}
}

func TestBumpAccessIncrementsCounter(t *testing.T) {
	db := openObsDB(t)
	store := db.Observations()
	ctx := context.Background()
	o := seed(t, store, "a", "b", memory.TypePattern)

	before, _ := store.Get(ctx, o.ID)
	_ = store.BumpAccess(ctx, o.ID)
	_ = store.BumpAccess(ctx, o.ID)
	after, _ := store.Get(ctx, o.ID)
	if after.AccessCount <= before.AccessCount {
		t.Errorf("access count didn't increase: %d → %d", before.AccessCount, after.AccessCount)
	}
}

func TestListByTitleSimilarity(t *testing.T) {
	db := openObsDB(t)
	store := db.Observations()
	ctx := context.Background()
	_ = seed(t, store, "sqlite wal mode", "ok", memory.TypePattern)
	_ = seed(t, store, "fts5 indexing", "ok", memory.TypePattern)

	hits, err := store.ListByTitleSimilarity(ctx, "default", "sqlite", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Errorf("expected 1 hit on 'sqlite', got %d", len(hits))
	}
}

func TestSearchAsOfHistorical(t *testing.T) {
	db := openObsDB(t)
	store := db.Observations()
	ctx := context.Background()

	now := time.Now().UTC()
	past := now.Add(-48 * time.Hour)

	o := seed(t, store, "old-fact", "we used to do this", memory.TypeDecision)
	_ = store.Invalidate(ctx, o.ID, now)

	// As-of historical: the fact was valid past 2d ago.
	hits, err := store.Search(ctx, memory.SearchInput{
		Query: "fact", AsOf: past, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Errorf("historical AsOf must surface fact valid at that time, got %d", len(hits))
	}
}
