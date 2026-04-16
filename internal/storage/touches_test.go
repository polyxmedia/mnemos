package storage_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func openTouchesDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestTouchesRecordAndHot(t *testing.T) {
	db := openTouchesDB(t)
	tstore := db.Touches()
	ctx := context.Background()

	files := []string{"main.go", "main.go", "main.go", "handler.go", "handler.go", "util.go"}
	for _, f := range files {
		if err := tstore.Record(ctx, memory.TouchInput{
			Project: "mnemos", AgentID: "default", Path: f,
		}); err != nil {
			t.Fatal(err)
		}
	}

	hot, err := tstore.Hot(ctx, "default", "mnemos", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hot) != 3 {
		t.Fatalf("expected 3 distinct paths, got %d", len(hot))
	}
	if hot[0].Path != "main.go" || hot[0].TouchCount < 3 {
		t.Errorf("main.go should be hottest with 3+ touches: %+v", hot)
	}
}

func TestTouchesRejectsEmptyPath(t *testing.T) {
	db := openTouchesDB(t)
	err := db.Touches().Record(context.Background(), memory.TouchInput{
		Project: "mnemos", Path: "",
	})
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestTouchesScopedByProject(t *testing.T) {
	db := openTouchesDB(t)
	ctx := context.Background()
	ts := db.Touches()

	_ = ts.Record(ctx, memory.TouchInput{Project: "p1", Path: "a.go"})
	_ = ts.Record(ctx, memory.TouchInput{Project: "p2", Path: "b.go"})

	hot1, _ := ts.Hot(ctx, "default", "p1", 10)
	hot2, _ := ts.Hot(ctx, "default", "p2", 10)
	if len(hot1) != 1 || hot1[0].Path != "a.go" {
		t.Errorf("p1 isolation failed: %+v", hot1)
	}
	if len(hot2) != 1 || hot2[0].Path != "b.go" {
		t.Errorf("p2 isolation failed: %+v", hot2)
	}
}
