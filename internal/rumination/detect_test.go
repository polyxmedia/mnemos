package rumination

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/skills"
)

// fakeSkillLister is an in-memory SkillLister for monitor tests. It lets
// tests inject the exact skill set they want to observe without touching
// the real store or the rest of the skills service surface.
type fakeSkillLister struct {
	items []skills.Skill
	err   error
}

func (f *fakeSkillLister) List(ctx context.Context, agentID string) ([]skills.Skill, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

func TestSkillEffectivenessMonitor_Detect(t *testing.T) {
	tests := []struct {
		name    string
		skills  []skills.Skill
		floor   float64
		minUses int
		want    []struct {
			targetID string
			severity Severity
		}
	}{
		{
			name: "skill moderately below floor fires at medium severity",
			skills: []skills.Skill{
				{ID: "s1", Name: "weak", UseCount: 12, SuccessCount: 2, Effectiveness: 0.20},
			},
			want: []struct {
				targetID string
				severity Severity
			}{{"s1", SeverityMedium}},
		},
		{
			name: "skill above floor is skipped",
			skills: []skills.Skill{
				{ID: "s1", Name: "ok", UseCount: 20, SuccessCount: 12, Effectiveness: 0.6},
			},
			want: nil,
		},
		{
			name: "skill below floor but under MinUses is skipped",
			skills: []skills.Skill{
				{ID: "s1", Name: "early", UseCount: 3, SuccessCount: 0, Effectiveness: 0.0},
			},
			want: nil,
		},
		{
			name: "severe failure gets high severity",
			skills: []skills.Skill{
				{ID: "s1", Name: "broken", UseCount: 20, SuccessCount: 1, Effectiveness: 0.05},
			},
			want: []struct {
				targetID string
				severity Severity
			}{{"s1", SeverityHigh}},
		},
		{
			name: "mild failure gets low severity",
			skills: []skills.Skill{
				{ID: "s1", Name: "edgy", UseCount: 15, SuccessCount: 4, Effectiveness: 0.27},
			},
			want: []struct {
				targetID string
				severity Severity
			}{{"s1", SeverityLow}},
		},
		{
			name: "multiple skills: monitor preserves input order, severity is independent",
			skills: []skills.Skill{
				{ID: "s1", Name: "a", UseCount: 20, SuccessCount: 2, Effectiveness: 0.1},
				{ID: "s2", Name: "b", UseCount: 20, SuccessCount: 10, Effectiveness: 0.5},
				{ID: "s3", Name: "c", UseCount: 12, SuccessCount: 3, Effectiveness: 0.25},
			},
			want: []struct {
				targetID string
				severity Severity
			}{{"s1", SeverityHigh}, {"s3", SeverityLow}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &SkillEffectivenessMonitor{
				Skills: &fakeSkillLister{items: tc.skills},
				now:    func() time.Time { return time.Unix(0, 0).UTC() },
			}
			got, err := m.Detect(context.Background())
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d candidates, want %d (%+v)", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				if got[i].TargetID != w.targetID {
					t.Errorf("candidate[%d] target=%s, want %s", i, got[i].TargetID, w.targetID)
				}
				if got[i].Severity != w.severity {
					t.Errorf("candidate[%d] severity=%s, want %s", i, got[i].Severity, w.severity)
				}
				if got[i].TargetKind != TargetSkill {
					t.Errorf("candidate[%d] kind=%s, want skill", i, got[i].TargetKind)
				}
				if got[i].MonitorName != "skill-effectiveness-floor" {
					t.Errorf("candidate[%d] monitor=%s", i, got[i].MonitorName)
				}
				if len(got[i].Evidence) == 0 {
					t.Errorf("candidate[%d] has no evidence", i)
				}
			}
		})
	}
}

func TestSkillEffectivenessMonitor_CustomThresholds(t *testing.T) {
	// Verify defaults are overridable: a stricter floor + looser min-uses
	// should fire where defaults would not, proving the knobs wire through.
	m := &SkillEffectivenessMonitor{
		Skills: &fakeSkillLister{items: []skills.Skill{
			{ID: "s1", Name: "marginal", UseCount: 5, SuccessCount: 2, Effectiveness: 0.4},
		}},
		Floor:   0.5,
		MinUses: 5,
	}
	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 candidate with custom thresholds, got %d", len(got))
	}
}

func TestSkillEffectivenessMonitor_ErrorPropagates(t *testing.T) {
	want := errors.New("store exploded")
	m := &SkillEffectivenessMonitor{Skills: &fakeSkillLister{err: want}}
	_, err := m.Detect(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want wrapping of %v", err, want)
	}
}

func TestCandidateID_Stable(t *testing.T) {
	a := candidateID("monitor-x", "target-y")
	b := candidateID("monitor-x", "target-y")
	if a != b {
		t.Errorf("candidateID not stable: %s vs %s", a, b)
	}
	c := candidateID("monitor-x", "target-z")
	if a == c {
		t.Errorf("candidateID collides across targets: %s", a)
	}
	if len(a) != len("rumination-")+12 {
		t.Errorf("candidateID format drift: %s", a)
	}
}
