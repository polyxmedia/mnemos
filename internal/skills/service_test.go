package skills_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func newSvc(t *testing.T) *skills.Service {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return skills.NewService(skills.Config{Store: db.Skills()})
}

func TestSkillSaveValidation(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()
	cases := []skills.SaveInput{
		{Description: "x", Procedure: "y"},
		{Name: "n", Procedure: "y"},
		{Name: "n", Description: "x"},
	}
	for i, in := range cases {
		if _, err := svc.Save(ctx, in); err == nil {
			t.Errorf("case %d expected error", i)
		}
	}
}

func TestSkillFeedbackBumpsEffectiveness(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()
	sk, err := svc.Save(ctx, skills.SaveInput{
		Name:        "test-skill",
		Description: "d",
		Procedure:   "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 2 successes, 1 failure → effectiveness 0.667
	_ = svc.RecordUse(ctx, skills.FeedbackInput{ID: sk.ID, Success: true})
	_ = svc.RecordUse(ctx, skills.FeedbackInput{ID: sk.ID, Success: true})
	_ = svc.RecordUse(ctx, skills.FeedbackInput{ID: sk.ID, Success: false})
	got, _ := svc.Get(ctx, sk.ID)
	if got.UseCount != 3 || got.SuccessCount != 2 {
		t.Errorf("counts wrong: use=%d success=%d", got.UseCount, got.SuccessCount)
	}
	if got.Effectiveness < 0.6 || got.Effectiveness > 0.7 {
		t.Errorf("expected ~0.667 effectiveness, got %f", got.Effectiveness)
	}
}
