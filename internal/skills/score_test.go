package skills_test

import (
	"context"
	"testing"

	"github.com/polyxmedia/mnemos/internal/skills"
)

func TestScoreShrinksLowVolumeEstimate(t *testing.T) {
	// A 1/1 skill should NOT outscore a 9/10 skill on the composite.
	svc := newSvc(t)
	ctx := context.Background()

	lowVol, _ := svc.Save(ctx, skills.SaveInput{
		Name: "low-vol", Description: "d", Procedure: "p",
	})
	_ = svc.RecordUse(ctx, skills.FeedbackInput{ID: lowVol.ID, Success: true})

	highVol, _ := svc.Save(ctx, skills.SaveInput{
		Name: "high-vol", Description: "d", Procedure: "p",
	})
	for i := 0; i < 9; i++ {
		_ = svc.RecordUse(ctx, skills.FeedbackInput{ID: highVol.ID, Success: true})
	}
	_ = svc.RecordUse(ctx, skills.FeedbackInput{ID: highVol.ID, Success: false})

	low, err := svc.Score(ctx, lowVol.ID)
	if err != nil {
		t.Fatal(err)
	}
	high, err := svc.Score(ctx, highVol.ID)
	if err != nil {
		t.Fatal(err)
	}

	if !(high.Score > low.Score) {
		t.Errorf("9/10 (%.3f) should outscore 1/1 (%.3f) after shrinkage",
			high.Score, low.Score)
	}
	if !(low.AdjustedEffectiveness < low.Effectiveness) {
		t.Errorf("1/1 raw effectiveness %.3f should shrink, got adjusted %.3f",
			low.Effectiveness, low.AdjustedEffectiveness)
	}
}

func TestScoreReportComponents(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()
	sk, _ := svc.Save(ctx, skills.SaveInput{
		Name: "x", Description: "d", Procedure: "p",
	})
	for i := 0; i < 5; i++ {
		_ = svc.RecordUse(ctx, skills.FeedbackInput{ID: sk.ID, Success: true})
	}
	rep, err := svc.Score(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.SkillID != sk.ID {
		t.Errorf("id mismatch: %s vs %s", rep.SkillID, sk.ID)
	}
	if rep.UseCount != 5 || rep.SuccessCount != 5 {
		t.Errorf("counts: use=%d success=%d", rep.UseCount, rep.SuccessCount)
	}
	if rep.Recency < 0.99 {
		t.Errorf("just-saved skill should have ~1.0 recency, got %.3f", rep.Recency)
	}
	if rep.Score <= 0 || rep.Score > 1 {
		t.Errorf("composite score must be in (0,1], got %.3f", rep.Score)
	}
}

func TestScoreMissingIDErrors(t *testing.T) {
	svc := newSvc(t)
	if _, err := svc.Score(context.Background(), ""); err == nil {
		t.Error("empty id should error")
	}
}

func TestScoreUnusedSkillScoresLow(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()
	sk, _ := svc.Save(ctx, skills.SaveInput{
		Name: "unused", Description: "d", Procedure: "p",
	})
	rep, err := svc.Score(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Effectiveness 0, but shrinkage pulls toward 0.5 prior * (count=0
	// → confidence=0 → fully toward prior). Composite dampened by
	// recency. Should land around 0.5 (no signal yet).
	if rep.AdjustedEffectiveness < 0.4 || rep.AdjustedEffectiveness > 0.6 {
		t.Errorf("unused skill should sit near 0.5 prior, got %.3f", rep.AdjustedEffectiveness)
	}
}
