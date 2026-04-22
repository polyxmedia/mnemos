package rumination

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/polyxmedia/mnemos/internal/skills"
)

// Monitor is one detection rule. Each monitor reports the candidates its
// own rule detected; the Service composes them. Keeping Monitor small
// lets us add new breach conditions (contradiction detection, stale
// skills, superseded-fact leaks) without touching the Service wiring.
type Monitor interface {
	Name() string
	Detect(ctx context.Context) ([]Candidate, error)
}

// SkillLister is the narrow read surface the effectiveness monitor
// depends on. Declared at the consumer so tests can pass an in-memory
// fake without implementing the full skills.Store surface.
type SkillLister interface {
	List(ctx context.Context, agentID string) ([]skills.Skill, error)
}

// SkillEffectivenessMonitor flags skills whose recorded effectiveness has
// fallen below Floor after at least MinUses uses. This is the highest-
// signal monitor: a skill that has been tried enough to be statistically
// meaningful and is still failing more often than not is the clearest
// case for a rumination pass.
//
// Defaults: Floor = 0.3, MinUses = 10. These match the prune-candidate
// thresholds already documented in docs/SKILLS.md so the intuition
// carries over cleanly: what SKILLS.md calls "candidate for pruning"
// becomes "candidate for rumination".
type SkillEffectivenessMonitor struct {
	Skills  SkillLister
	AgentID string
	Floor   float64
	MinUses int

	// now is injected so tests can freeze DetectedAt. Unset in production.
	now func() time.Time
}

// Name implements Monitor.
func (m *SkillEffectivenessMonitor) Name() string {
	return "skill-effectiveness-floor"
}

// Detect returns a Candidate for every skill that breached the floor.
// Severity scales with how far below the floor the skill has fallen.
func (m *SkillEffectivenessMonitor) Detect(ctx context.Context) ([]Candidate, error) {
	floor := m.Floor
	if floor <= 0 {
		floor = 0.3
	}
	minUses := m.MinUses
	if minUses <= 0 {
		minUses = 10
	}
	clock := m.now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}

	list, err := m.Skills.List(ctx, m.AgentID)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}

	out := make([]Candidate, 0, len(list))
	for _, sk := range list {
		if sk.UseCount < minUses {
			continue
		}
		if sk.Effectiveness >= floor {
			continue
		}
		out = append(out, Candidate{
			ID:          candidateID(m.Name(), sk.ID),
			MonitorName: m.Name(),
			Severity:    effectivenessSeverity(sk.Effectiveness, floor),
			Reason: fmt.Sprintf("effectiveness %.2f after %d uses (floor %.2f)",
				sk.Effectiveness, sk.UseCount, floor),
			TargetKind: TargetSkill,
			TargetID:   sk.ID,
			Evidence: []Evidence{{
				Label: "effectiveness history",
				Content: fmt.Sprintf(
					"%d uses recorded, %d succeeded (%.2f). Floor for rumination: %.2f.",
					sk.UseCount, sk.SuccessCount, sk.Effectiveness, floor),
				Source: sk.ID,
			}},
			DetectedAt: clock(),
		})
	}
	return out, nil
}

// effectivenessSeverity buckets how far below the floor a skill has drifted
// into three severity levels so the prewarm can surface the urgent ones
// first.
func effectivenessSeverity(eff, floor float64) Severity {
	if eff < floor*0.5 {
		return SeverityHigh
	}
	if eff < floor*0.8 {
		return SeverityMedium
	}
	return SeverityLow
}

// candidateID produces a stable 12-char hex ID per (monitor, target). The
// prefix keeps these visibly distinct from observation/skill IDs in logs
// and agent-facing output. Matches the hash-truncation convention from
// dream.groupHash so memory-wide ID conventions stay consistent.
func candidateID(monitor, target string) string {
	sum := sha256.Sum256([]byte(monitor + "|" + target))
	return "rumination-" + hex.EncodeToString(sum[:])[:12]
}
