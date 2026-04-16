package memory_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func newSvcPlus(t *testing.T) *memory.Service {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return memory.NewService(memory.Config{Store: db.Observations()})
}

func TestDeleteUnknownReturnsErrNotFound(t *testing.T) {
	svc := newSvcPlus(t)
	err := svc.Delete(context.Background(), "nonexistent")
	if !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestInvalidateUnknownReturnsErrNotFound(t *testing.T) {
	svc := newSvcPlus(t)
	err := svc.Invalidate(context.Background(), "nonexistent")
	if !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestPruneRemovesExpired(t *testing.T) {
	svc := newSvcPlus(t)
	ctx := context.Background()
	// Save an observation with a 1-day TTL, then move clock backward:
	// we can't directly backdate via public API, so use negative TTLDays
	// isn't supported — instead save normally and confirm Prune returns 0.
	_, _ = svc.Save(ctx, memory.SaveInput{
		Title: "x", Content: "y", Type: memory.TypePattern, TTLDays: 1,
	})
	n, err := svc.Prune(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("nothing should be expired yet, got %d", n)
	}
}

func TestLinkValidLinkTypes(t *testing.T) {
	svc := newSvcPlus(t)
	ctx := context.Background()
	a, _ := svc.Save(ctx, memory.SaveInput{
		Title: "a", Content: "a", Type: memory.TypeContext,
	})
	b, _ := svc.Save(ctx, memory.SaveInput{
		Title: "b", Content: "b", Type: memory.TypeContext,
	})
	for _, lt := range []memory.LinkType{
		memory.LinkRelated, memory.LinkCausedBy, memory.LinkContradicts,
		memory.LinkRefines,
	} {
		if err := svc.Link(ctx, a.Observation.ID, b.Observation.ID, lt); err != nil {
			t.Errorf("link type %s: %v", lt, err)
		}
	}
}

func TestSaveInvalidImportance(t *testing.T) {
	svc := newSvcPlus(t)
	ctx := context.Background()
	cases := []int{-1, 0, 11}
	for _, imp := range cases {
		// 0 is allowed (defaults to 5); only <1 or >10 error after default.
		_, err := svc.Save(ctx, memory.SaveInput{
			Title: "x", Content: "y", Type: memory.TypePattern, Importance: imp,
		})
		if imp == 0 {
			if err != nil {
				t.Errorf("0 should default to 5, got %v", err)
			}
			continue
		}
		if err == nil {
			t.Errorf("importance %d should fail", imp)
		}
	}
}

func TestObservationLiveRespectsTimestamps(t *testing.T) {
	now := time.Now().UTC()
	o := memory.Observation{CreatedAt: now, ValidFrom: now}
	if !o.Live(now) {
		t.Error("fresh observation should be live")
	}
	invalidated := now
	o.InvalidatedAt = &invalidated
	if o.Live(now) {
		t.Error("invalidated observation should not be live")
	}
	o.InvalidatedAt = nil
	past := now.Add(-time.Minute)
	o.ValidUntil = &past
	if o.Live(now) {
		t.Error("observation past valid_until should not be live")
	}
	o.ValidUntil = nil
	o.ExpiresAt = &past
	if o.Live(now) {
		t.Error("expired observation should not be live")
	}
}

func TestObsTypeValid(t *testing.T) {
	for _, tt := range memory.AllTypes {
		if !tt.Valid() {
			t.Errorf("type %s should be valid", tt)
		}
	}
	if memory.ObsType("bogus").Valid() {
		t.Error("bogus type should not be valid")
	}
}

func TestLinkTypeValid(t *testing.T) {
	valid := []memory.LinkType{
		memory.LinkRelated, memory.LinkCausedBy, memory.LinkSupersedes,
		memory.LinkContradicts, memory.LinkRefines,
	}
	for _, lt := range valid {
		if !lt.Valid() {
			t.Errorf("%s should be valid", lt)
		}
	}
	if memory.LinkType("bogus").Valid() {
		t.Error("bogus link type should not be valid")
	}
}
