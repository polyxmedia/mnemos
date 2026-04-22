package rumination

import (
	"context"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/skills"
)

func TestStaleSkillMonitor_FiresOnOldUnderperforming(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		skill     skills.Skill
		wantFires bool
		wantSev   Severity
	}{
		{
			name: "old + low effectiveness fires",
			skill: skills.Skill{
				ID: "s1", Name: "dusty", UpdatedAt: now.AddDate(0, 0, -200),
				UseCount: 10, SuccessCount: 3, Effectiveness: 0.30,
			},
			wantFires: true,
			wantSev:   SeverityMedium, // 200 / 90 ≈ 2.2 → medium
		},
		{
			name: "old + zero uses fires (never earned its slot)",
			skill: skills.Skill{
				ID: "s1", Name: "forgotten", UpdatedAt: now.AddDate(0, 0, -365),
				UseCount: 0, SuccessCount: 0, Effectiveness: 0,
			},
			wantFires: true,
			wantSev:   SeverityHigh, // 365 / 90 ≈ 4 → high
		},
		{
			name: "old but effective does not fire",
			skill: skills.Skill{
				ID: "s1", Name: "aging well", UpdatedAt: now.AddDate(0, 0, -200),
				UseCount: 10, SuccessCount: 9, Effectiveness: 0.90,
			},
			wantFires: false,
		},
		{
			name: "recent + low effectiveness does not fire (age is the other half)",
			skill: skills.Skill{
				ID: "s1", Name: "young fail", UpdatedAt: now.AddDate(0, 0, -5),
				UseCount: 5, SuccessCount: 0, Effectiveness: 0,
			},
			wantFires: false,
		},
		{
			name: "just under stale cutoff does not fire",
			skill: skills.Skill{
				ID: "s1", Name: "borderline", UpdatedAt: now.AddDate(0, 0, -89),
				UseCount: 5, SuccessCount: 0, Effectiveness: 0,
			},
			wantFires: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &StaleSkillMonitor{
				Skills: &fakeSkillLister{items: []skills.Skill{tc.skill}},
				now:    func() time.Time { return now },
			}
			got, err := m.Detect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantFires && len(got) != 1 {
				t.Fatalf("want 1 candidate, got %d", len(got))
			}
			if !tc.wantFires && len(got) != 0 {
				t.Fatalf("want 0 candidates, got %+v", got)
			}
			if tc.wantFires && got[0].Severity != tc.wantSev {
				t.Errorf("severity = %s, want %s", got[0].Severity, tc.wantSev)
			}
		})
	}
}

func TestStaleSkillMonitor_CustomThresholds(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	m := &StaleSkillMonitor{
		Skills: &fakeSkillLister{items: []skills.Skill{
			{ID: "s1", UpdatedAt: now.AddDate(0, 0, -8), UseCount: 3, SuccessCount: 1, Effectiveness: 0.33},
		}},
		StaleDays:        7,
		MaxEffectiveness: 0.8,
		now:              func() time.Time { return now },
	}
	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("stricter thresholds should fire on a skill that defaults miss: %d", len(got))
	}
}
