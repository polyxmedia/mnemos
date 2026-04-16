package skills_test

import (
	"context"
	"testing"

	"github.com/polyxmedia/mnemos/internal/skills"
)

func TestMatchRequiresQuery(t *testing.T) {
	svc := newSvc(t)
	if _, err := svc.Match(context.Background(), skills.MatchInput{}); err == nil {
		t.Error("expected error on empty query")
	}
}

func TestRecordUseValidation(t *testing.T) {
	svc := newSvc(t)
	if err := svc.RecordUse(context.Background(), skills.FeedbackInput{}); err == nil {
		t.Error("expected error on empty id")
	}
}

func TestListReturnsAllSkills(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()
	for _, name := range []string{"a", "b", "c"} {
		if _, err := svc.Save(ctx, skills.SaveInput{
			Name: name, Description: "x", Procedure: "y",
		}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := svc.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Errorf("want 3 skills, got %d", len(list))
	}
}
