package rumination

import (
	"context"
	"fmt"
	"time"
)

// StaleSkillMonitor flags skills that have not been touched in a long
// time and have not accumulated meaningful effectiveness. A skill that
// sat unused for months with zero or near-zero success rate is either
// (a) never needed — retire it, or (b) silently failing — review and
// revise. Either outcome is a net positive for the store's signal-to-
// noise ratio.
//
// Defaults: StaleDays = 90, MaxEffectiveness = 0.5. A skill with ≥ 0.5
// effectiveness that just has not seen use lately is probably fine — age
// alone is not a problem. The combined filter (old AND underperforming)
// is what makes the monitor conservative.
type StaleSkillMonitor struct {
	Skills           SkillLister
	AgentID          string
	StaleDays        int
	MaxEffectiveness float64

	now func() time.Time // test override
}

// Name implements Monitor.
func (m *StaleSkillMonitor) Name() string { return "skill-stale" }

// Detect returns Candidates for every stale, underperforming skill.
func (m *StaleSkillMonitor) Detect(ctx context.Context) ([]Candidate, error) {
	staleDays := m.StaleDays
	if staleDays <= 0 {
		staleDays = 90
	}
	floor := m.MaxEffectiveness
	if floor <= 0 {
		floor = 0.5
	}
	clock := m.now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	now := clock()
	cutoff := now.AddDate(0, 0, -staleDays)

	list, err := m.Skills.List(ctx, m.AgentID)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	out := make([]Candidate, 0, len(list))
	for _, sk := range list {
		// Stale: UpdatedAt before cutoff.
		if !sk.UpdatedAt.Before(cutoff) {
			continue
		}
		// Underperforming: effectiveness below floor, OR never used at all
		// (both suggest the skill is not earning its slot in retrieval).
		if sk.UseCount > 0 && sk.Effectiveness >= floor {
			continue
		}

		ageDays := int(now.Sub(sk.UpdatedAt).Hours() / 24)
		out = append(out, Candidate{
			ID:          candidateID(m.Name(), sk.ID),
			MonitorName: m.Name(),
			Severity:    staleSeverity(ageDays, staleDays),
			Reason: fmt.Sprintf("unused %d days · %d uses · effectiveness %.2f",
				ageDays, sk.UseCount, sk.Effectiveness),
			TargetKind: TargetSkill,
			TargetID:   sk.ID,
			Evidence: []Evidence{{
				Label: "usage history",
				Content: fmt.Sprintf(
					"last touched %s (%d days ago). %d uses, %d successes, effectiveness %.2f.",
					sk.UpdatedAt.Format("2006-01-02"), ageDays,
					sk.UseCount, sk.SuccessCount, sk.Effectiveness),
				Source: sk.ID,
			}},
			DetectedAt: now,
		})
	}
	return out, nil
}

// staleSeverity scales with how far past the cutoff a skill has drifted.
// 2× overdue counts as medium; 3× as high. The base level is low because
// staleness alone is a weaker signal than outright effectiveness failure.
func staleSeverity(ageDays, cutoffDays int) Severity {
	if cutoffDays <= 0 {
		return SeverityLow
	}
	ratio := float64(ageDays) / float64(cutoffDays)
	switch {
	case ratio >= 3:
		return SeverityHigh
	case ratio >= 2:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

