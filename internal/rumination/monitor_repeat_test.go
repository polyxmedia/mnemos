package rumination

import (
	"context"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// fakeCorrectionLister returns whatever the caller pre-loaded. The
// ListByProject signature matches CorrectionLister so the monitor can
// depend on the narrow interface directly without the full memory.Reader.
type fakeCorrectionLister struct {
	items []memory.Observation
	err   error
}

func (f *fakeCorrectionLister) ListByProject(ctx context.Context, agentID, project string, t memory.ObsType, limit int) ([]memory.Observation, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

func TestCorrectionRepeatMonitor_FiresWhenSkillStopsWorking(t *testing.T) {
	skillCreated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	after := skillCreated.Add(24 * time.Hour)

	skl := &fakeSkillLister{items: []skills.Skill{{
		ID: "sk-oauth", Name: "auto: oauth (p)", Tags: []string{"oauth", "auto-promoted"},
		CreatedAt: skillCreated, UpdatedAt: skillCreated,
	}}}

	// Three corrections on "oauth" after skill creation → threshold hit.
	corr := &fakeCorrectionLister{items: []memory.Observation{
		{ID: "c1", Title: "another oauth problem", Tags: []string{"oauth"},
			Type: memory.TypeCorrection, Project: "p", CreatedAt: after},
		{ID: "c2", Title: "oauth still wrong", Tags: []string{"oauth"},
			Type: memory.TypeCorrection, Project: "p", CreatedAt: after.Add(time.Hour)},
		{ID: "c3", Title: "oauth failing again", Tags: []string{"oauth"},
			Type: memory.TypeCorrection, Project: "p", CreatedAt: after.Add(2 * time.Hour)},
	}}

	m := &CorrectionRepeatUnderSkillMonitor{
		Corrections: corr, Skills: skl,
		now: func() time.Time { return after.Add(3 * time.Hour) },
	}
	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(got))
	}
	if got[0].TargetID != "sk-oauth" {
		t.Errorf("candidate target = %s", got[0].TargetID)
	}
	if got[0].MonitorName != "correction-repeat-under-skill" {
		t.Errorf("monitor name = %s", got[0].MonitorName)
	}
}

func TestCorrectionRepeatMonitor_IgnoresPreCreationCorrections(t *testing.T) {
	// The whole point of this monitor: only corrections recorded AFTER
	// the skill was created count. Pre-creation corrections are the ones
	// that justified the skill in the first place (they got promoted into
	// it); counting them again would be double-counting.
	skillCreated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	before := skillCreated.Add(-24 * time.Hour)

	skl := &fakeSkillLister{items: []skills.Skill{{
		ID: "sk-oauth", Tags: []string{"oauth"}, CreatedAt: skillCreated, UpdatedAt: skillCreated,
	}}}
	corr := &fakeCorrectionLister{items: []memory.Observation{
		{ID: "c1", Tags: []string{"oauth"}, Type: memory.TypeCorrection, Project: "p", CreatedAt: before},
		{ID: "c2", Tags: []string{"oauth"}, Type: memory.TypeCorrection, Project: "p", CreatedAt: before},
		{ID: "c3", Tags: []string{"oauth"}, Type: memory.TypeCorrection, Project: "p", CreatedAt: before},
	}}

	m := &CorrectionRepeatUnderSkillMonitor{Corrections: corr, Skills: skl}
	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("pre-creation corrections should not fire, got %+v", got)
	}
}

func TestCorrectionRepeatMonitor_BelowThresholdSilent(t *testing.T) {
	skillCreated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	after := skillCreated.Add(24 * time.Hour)

	skl := &fakeSkillLister{items: []skills.Skill{{ID: "sk", Tags: []string{"oauth"}, CreatedAt: skillCreated}}}
	corr := &fakeCorrectionLister{items: []memory.Observation{
		{ID: "c1", Tags: []string{"oauth"}, Type: memory.TypeCorrection, CreatedAt: after},
		{ID: "c2", Tags: []string{"oauth"}, Type: memory.TypeCorrection, CreatedAt: after},
	}}
	m := &CorrectionRepeatUnderSkillMonitor{Corrections: corr, Skills: skl}
	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("2 corrections is below default threshold (3), should not fire: %+v", got)
	}
}

func TestCorrectionRepeatMonitor_NoMatchingSkill(t *testing.T) {
	// Three oauth corrections but no skill with that tag → this monitor
	// doesn't fire (promotion is a different concern, handled in dream).
	corr := &fakeCorrectionLister{items: []memory.Observation{
		{ID: "c1", Tags: []string{"oauth"}, Type: memory.TypeCorrection, CreatedAt: time.Now()},
		{ID: "c2", Tags: []string{"oauth"}, Type: memory.TypeCorrection, CreatedAt: time.Now()},
		{ID: "c3", Tags: []string{"oauth"}, Type: memory.TypeCorrection, CreatedAt: time.Now()},
	}}
	m := &CorrectionRepeatUnderSkillMonitor{Corrections: corr, Skills: &fakeSkillLister{}}
	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("no matching skill should be silent: %+v", got)
	}
}

func TestIsStructuralCorrectionTag(t *testing.T) {
	cases := map[string]bool{
		"oauth":                        false,
		"auto-promoted":                true,
		"promoted-origin:abc123":       true,
		"project:mnemos":               true,
		"ruminated-from:rumination-x":  true,
		"":                             false, // empty is handled elsewhere
	}
	for tag, want := range cases {
		if got := isStructuralCorrectionTag(tag); got != want {
			t.Errorf("isStructuralCorrectionTag(%q) = %v, want %v", tag, got, want)
		}
	}
}
