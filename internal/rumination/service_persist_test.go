package rumination

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/skills"
)

// fakeStore is an in-memory Store for service-level tests. It keeps enough
// state to exercise the Persist / Pending / Resolve / Dismiss flow without
// pulling in SQLite.
type fakeStore struct {
	mu    sync.Mutex
	items map[string]*Candidate
}

func newFakeStore() *fakeStore {
	return &fakeStore{items: map[string]*Candidate{}}
}

func (f *fakeStore) Upsert(ctx context.Context, c Candidate) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// dedup on (monitor, kind, target), not on ID — mirrors SQLite.
	for _, existing := range f.items {
		if existing.MonitorName == c.MonitorName &&
			existing.TargetKind == c.TargetKind &&
			existing.TargetID == c.TargetID {
			existing.Severity = c.Severity
			existing.Reason = c.Reason
			existing.Evidence = c.Evidence
			existing.UpdatedAt = time.Now().UTC()
			return false, nil
		}
	}
	c.Status = StatusPending
	c.UpdatedAt = time.Now().UTC()
	clone := c
	f.items[c.ID] = &clone
	return true, nil
}

func (f *fakeStore) Get(ctx context.Context, id string) (*Candidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	clone := *c
	return &clone, nil
}

func (f *fakeStore) Pending(ctx context.Context, limit int) ([]Candidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Candidate
	for _, c := range f.items {
		if c.Status == StatusPending {
			out = append(out, *c)
		}
	}
	// severity desc
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j].Severity > out[i].Severity {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeStore) PendingByTarget(ctx context.Context, kind TargetKind, targetID string) ([]Candidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Candidate
	for _, c := range f.items {
		if c.Status == StatusPending && c.TargetKind == kind && c.TargetID == targetID {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (f *fakeStore) Resolve(ctx context.Context, id, resolvedBy string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if resolvedBy == "" {
		return errors.New("resolved_by required")
	}
	c, ok := f.items[id]
	if !ok {
		return ErrNotFound
	}
	if c.Status == StatusDismissed {
		return errors.New("cannot resolve dismissed candidate")
	}
	c.Status = StatusResolved
	c.ResolvedBy = resolvedBy
	c.ResolvedAt = at
	return nil
}

func (f *fakeStore) Dismiss(ctx context.Context, id, reason string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if reason == "" {
		return errors.New("reason required")
	}
	c, ok := f.items[id]
	if !ok {
		return ErrNotFound
	}
	if c.Status != StatusPending {
		return errors.New("not pending")
	}
	c.Status = StatusDismissed
	c.DismissedReason = reason
	c.DismissedAt = at
	return nil
}

func (f *fakeStore) Counts(ctx context.Context) (Counts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out Counts
	for _, c := range f.items {
		switch c.Status {
		case StatusPending:
			out.Pending++
		case StatusResolved:
			out.Resolved++
		case StatusDismissed:
			out.Dismissed++
		}
	}
	return out, nil
}

// fixedClock lets us freeze now() in service tests so Resolve/Dismiss
// timestamps are deterministic.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestService_PersistDetected(t *testing.T) {
	lister := &fakeSkillLister{items: []skills.Skill{
		{ID: "s1", Name: "weak", UseCount: 20, SuccessCount: 2, Effectiveness: 0.10},
		{ID: "s2", Name: "ok", UseCount: 20, SuccessCount: 14, Effectiveness: 0.70},
	}}
	store := newFakeStore()
	svc := NewService(Config{
		Monitors: []Monitor{&SkillEffectivenessMonitor{Skills: lister}},
		Store:    store,
	})

	ins, upd, err := svc.PersistDetected(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ins != 1 || upd != 0 {
		t.Errorf("want 1 insert 0 updates, got %d inserts %d updates", ins, upd)
	}

	// Second pass: same detection, should update not insert.
	ins, upd, err = svc.PersistDetected(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ins != 0 || upd != 1 {
		t.Errorf("second pass should all-update, got %d inserts %d updates", ins, upd)
	}
}

func TestService_PersistDetected_NoStore(t *testing.T) {
	svc := NewService(Config{Monitors: []Monitor{&SkillEffectivenessMonitor{Skills: &fakeSkillLister{}}}})
	_, _, err := svc.PersistDetected(context.Background())
	if err == nil {
		t.Errorf("want error when Store is nil")
	}
}

func TestService_PackByID(t *testing.T) {
	store := newFakeStore()
	reader := &fakeSkillReader{items: []skills.Skill{{
		ID: "sk1", Name: "retry on 401", Procedure: "retry the request",
	}}}
	svc := NewService(Config{Skills: reader, Store: store})

	// Seed a candidate.
	c := Candidate{
		ID:          "rumination-abc",
		MonitorName: "skill-effectiveness-floor",
		Severity:    SeverityHigh,
		Reason:      "r",
		TargetKind:  TargetSkill,
		TargetID:    "sk1",
	}
	if _, err := store.Upsert(context.Background(), c); err != nil {
		t.Fatal(err)
	}

	block, err := svc.PackByID(context.Background(), "rumination-abc")
	if err != nil {
		t.Fatal(err)
	}
	if block.Target.Name != "retry on 401" {
		t.Errorf("unexpected target name %s", block.Target.Name)
	}
}

func TestService_Resolve_Dismiss_Flow(t *testing.T) {
	store := newFakeStore()
	frozen := time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC)
	svc := NewService(Config{Store: store, Now: fixedClock(frozen)})

	store.Upsert(context.Background(), Candidate{
		ID: "c1", MonitorName: "m", Severity: SeverityMedium,
		TargetKind: TargetSkill, TargetID: "s1", Reason: "r",
	})
	store.Upsert(context.Background(), Candidate{
		ID: "c2", MonitorName: "m", Severity: SeverityLow,
		TargetKind: TargetSkill, TargetID: "s2", Reason: "r",
	})

	// Resolve c1.
	if err := svc.Resolve(context.Background(), "c1", "sk-new"); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(context.Background(), "c1")
	if got.Status != StatusResolved || got.ResolvedBy != "sk-new" || !got.ResolvedAt.Equal(frozen) {
		t.Errorf("resolve didn't flow correctly: %+v", got)
	}

	// Dismiss c2.
	if err := svc.Dismiss(context.Background(), "c2", "noise"); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Get(context.Background(), "c2")
	if got.Status != StatusDismissed || got.DismissedReason != "noise" || !got.DismissedAt.Equal(frozen) {
		t.Errorf("dismiss didn't flow correctly: %+v", got)
	}

	counts, _ := svc.Counts(context.Background())
	if counts.Resolved != 1 || counts.Dismissed != 1 || counts.Pending != 0 {
		t.Errorf("counts after resolve+dismiss: %+v", counts)
	}
}

func TestService_Pending_Wrappers_RequireStore(t *testing.T) {
	svc := NewService(Config{})
	if _, err := svc.Pending(context.Background(), 0); err == nil {
		t.Errorf("Pending without store should error")
	}
	if _, err := svc.PendingByTarget(context.Background(), TargetSkill, "x"); err == nil {
		t.Errorf("PendingByTarget without store should error")
	}
	if err := svc.Resolve(context.Background(), "c", "r"); err == nil {
		t.Errorf("Resolve without store should error")
	}
	if err := svc.Dismiss(context.Background(), "c", "r"); err == nil {
		t.Errorf("Dismiss without store should error")
	}
	if _, err := svc.Counts(context.Background()); err == nil {
		t.Errorf("Counts without store should error")
	}
	if _, err := svc.PackByID(context.Background(), "c"); err == nil {
		t.Errorf("PackByID without store should error")
	}
}
