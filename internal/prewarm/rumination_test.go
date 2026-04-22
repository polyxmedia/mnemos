package prewarm_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// fakeRumReader is an in-memory RuminationReader for prewarm tests. It
// avoids the SQLite dependency so we can assert the rendering path in
// isolation: we control exactly which candidates the prewarm sees.
type fakeRumReader struct {
	pending       []rumination.Candidate
	byTarget      map[string][]rumination.Candidate // key: kind + ":" + id
	pendingErr    error
	byTargetErr   error
	pendingCalls  int
	byTargetCalls int
}

func (f *fakeRumReader) Pending(ctx context.Context, limit int) ([]rumination.Candidate, error) {
	f.pendingCalls++
	if f.pendingErr != nil {
		return nil, f.pendingErr
	}
	out := f.pending
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeRumReader) PendingByTarget(ctx context.Context, kind rumination.TargetKind, id string) ([]rumination.Candidate, error) {
	f.byTargetCalls++
	if f.byTargetErr != nil {
		return nil, f.byTargetErr
	}
	return f.byTarget[string(kind)+":"+id], nil
}

func openPrewarmDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestPrewarm_PendingRuminationSection(t *testing.T) {
	db := openPrewarmDB(t)
	// Seed a convention so we have at least one section and the block
	// isn't empty.
	mem := memory.NewService(memory.Config{Store: db.Observations()})
	_, _ = mem.Save(context.Background(), memory.SaveInput{
		Title: "wrap errors", Content: "use fmt.Errorf with %w",
		Type: memory.TypeConvention, Project: "p", Rationale: "errors.Is",
	})

	reader := &fakeRumReader{
		pending: []rumination.Candidate{{
			ID: "rumination-aaaaaaaaaaaa", MonitorName: "skill-effectiveness-floor",
			Severity: rumination.SeverityHigh, Reason: "effectiveness 0.10 after 20 uses (floor 0.30)",
			TargetKind: rumination.TargetSkill, TargetID: "sk1",
		}},
	}

	svc := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(),
		Sessions:     db.Sessions(),
		Skills:       db.Skills(),
		Touches:      db.Touches(),
		Rumination:   reader,
	})

	block, err := svc.Build(context.Background(), prewarm.Request{
		Mode: prewarm.ModeSessionStart, Project: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reader.pendingCalls == 0 {
		t.Errorf("prewarm should have queried Pending at least once")
	}
	for _, want := range []string{"## pending reviews", "[high]", "skill-effectiveness-floor", "rumination-aaaaaaaaaaaa"} {
		if !strings.Contains(block.Text, want) {
			t.Errorf("prewarm missing %q:\n%s", want, block.Text)
		}
	}
}

func TestPrewarm_SkipsRuminationSectionWhenEmpty(t *testing.T) {
	db := openPrewarmDB(t)
	reader := &fakeRumReader{pending: nil}
	svc := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(), Sessions: db.Sessions(),
		Skills: db.Skills(), Touches: db.Touches(),
		Rumination: reader,
	})
	block, err := svc.Build(context.Background(), prewarm.Request{
		Mode: prewarm.ModeSessionStart, Project: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(block.Text, "pending reviews") {
		t.Errorf("empty rumination queue should omit the section:\n%s", block.Text)
	}
}

func TestPrewarm_NilRuminationLeavesBlockClean(t *testing.T) {
	db := openPrewarmDB(t)
	svc := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(), Sessions: db.Sessions(),
		Skills: db.Skills(), Touches: db.Touches(),
	})
	// No rumination service wired; must not error, must not include the
	// section. This is the backwards-compat invariant.
	block, err := svc.Build(context.Background(), prewarm.Request{
		Mode: prewarm.ModeSessionStart, Project: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(block.Text, "pending reviews") {
		t.Errorf("nil rumination should omit the section:\n%s", block.Text)
	}
}

func TestPrewarm_SkillMatchInlineBadge(t *testing.T) {
	db := openPrewarmDB(t)
	skl := skills.NewService(skills.Config{Store: db.Skills()})
	saved, err := skl.Save(context.Background(), skills.SaveInput{
		Name: "retry on 401", Description: "retry auth failures",
		Procedure: "retry the request",
	})
	if err != nil {
		t.Fatal(err)
	}

	reader := &fakeRumReader{
		byTarget: map[string][]rumination.Candidate{
			"skill:" + saved.ID: {{ID: "rumination-bbbbbbbbbbbb", Severity: rumination.SeverityMedium}},
		},
	}
	svc := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(),
		Sessions:     db.Sessions(),
		Skills:       db.Skills(),
		Touches:      db.Touches(),
		Rumination:   reader,
	})

	block, err := svc.Build(context.Background(), prewarm.Request{
		Mode: prewarm.ModeSessionStart, Project: "p",
		Goal: "401 oauth retry",
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(block.Text, "[rumination pending: rumination-bbbbbbbbbbbb]") {
		t.Errorf("matched skill should carry inline rumination badge:\n%s", block.Text)
	}
}

func TestPrewarm_RuminationErrorIsNonFatal(t *testing.T) {
	db := openPrewarmDB(t)
	// Sentinel error from the rumination reader; prewarm must still build
	// a usable block. Rumination is best-effort; never fatal.
	reader := &fakeRumReader{pendingErr: context.DeadlineExceeded}
	svc := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(), Sessions: db.Sessions(),
		Skills: db.Skills(), Touches: db.Touches(),
		Rumination: reader,
	})
	block, err := svc.Build(context.Background(), prewarm.Request{
		Mode: prewarm.ModeSessionStart, Project: "p",
	})
	if err != nil {
		t.Fatalf("rumination error should be swallowed, got %v", err)
	}
	if block == nil {
		t.Fatal("block should still be built")
	}
}
