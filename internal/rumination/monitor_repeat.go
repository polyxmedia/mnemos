package rumination

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// CorrectionRepeatUnderSkillMonitor flags skills whose topic has
// accumulated new corrections *after* the skill was created. If a skill
// claims to encode the rule and corrections on that same topic keep
// landing, the skill is not doing the work — either its guidance is
// wrong or the agent isn't following it. Either way, the skill is a
// rumination target.
//
// This is the dual of the dream-pass promotion step: promotion creates a
// skill when three corrections cluster; this monitor complains when three
// *more* corrections on the same cluster land after the skill exists.
//
// Label extraction mirrors the promotion path so the clustering key is
// consistent — agents tagging corrections the same way see the same
// behaviour from both sides of the loop.
type CorrectionRepeatUnderSkillMonitor struct {
	Corrections CorrectionLister
	Skills      SkillLister
	AgentID     string

	// RepeatThreshold is how many corrections must arrive after the
	// skill's CreatedAt before the monitor fires. Defaults to 3, matching
	// the promotion threshold so "three more corrections" is the symmetric
	// complaint against the rule promotion made.
	RepeatThreshold int

	now func() time.Time
}

// CorrectionLister is the narrow read surface for corrections. Declared
// at the consumer so tests don't need the full memory.Reader.
type CorrectionLister interface {
	ListByProject(ctx context.Context, agentID, project string, obsType memory.ObsType, limit int) ([]memory.Observation, error)
}

// Name implements Monitor.
func (m *CorrectionRepeatUnderSkillMonitor) Name() string {
	return "correction-repeat-under-skill"
}

// Detect scans live corrections, groups them by (agent, project, label),
// looks up any skill tagged with that label (or with an `auto-promoted`
// provenance tag referencing the same cluster hash), and fires when the
// count of post-creation corrections meets RepeatThreshold.
func (m *CorrectionRepeatUnderSkillMonitor) Detect(ctx context.Context) ([]Candidate, error) {
	threshold := m.RepeatThreshold
	if threshold <= 0 {
		threshold = 3
	}
	clock := m.now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	now := clock()

	// Bulk fetch all live corrections. Corrections are rarer than generic
	// observations; 10k is generous and keeps the implementation simple.
	corrections, err := m.Corrections.ListByProject(ctx, m.AgentID, "", memory.TypeCorrection, 10000)
	if err != nil {
		return nil, fmt.Errorf("list corrections: %w", err)
	}

	// Group corrections by (agent, project, label). This is the same
	// clustering key the promotion step uses; see dream/promote.go.
	type key struct{ agent, project, label string }
	groups := map[key][]memory.Observation{}
	for _, c := range corrections {
		label := firstNonStructuralTag(c.Tags)
		if label == "" {
			label = firstWordsOfTitle(c.Title, 3)
		}
		if label == "" {
			continue
		}
		agent := c.AgentID
		if agent == "" {
			agent = "default"
		}
		groups[key{agent: agent, project: c.Project, label: strings.ToLower(label)}] = append(
			groups[key{agent: agent, project: c.Project, label: strings.ToLower(label)}], c)
	}

	// Index skills by label (from tags) so we can look them up per group.
	skillList, err := m.Skills.List(ctx, m.AgentID)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	skillByLabel := make(map[string]skills.Skill, len(skillList))
	for _, sk := range skillList {
		for _, t := range sk.Tags {
			if tag := strings.TrimPrefix(t, "project:"); tag != t {
				continue
			}
			// Unstructured tags count as labels — same rule as the
			// promotion step: first meaningful tag wins.
			if isStructuralSkillTag(t) {
				continue
			}
			lowered := strings.ToLower(t)
			// Only set first occurrence; later duplicates under different
			// projects should not overwrite.
			if _, ok := skillByLabel[lowered]; !ok {
				skillByLabel[lowered] = sk
			}
		}
	}

	out := make([]Candidate, 0)
	for k, obs := range groups {
		sk, ok := skillByLabel[k.label]
		if !ok {
			continue
		}
		// Count corrections recorded AFTER the skill was created — those
		// are the ones proving the skill isn't holding up.
		after := 0
		for _, c := range obs {
			if c.CreatedAt.After(sk.CreatedAt) {
				after++
			}
		}
		if after < threshold {
			continue
		}

		out = append(out, Candidate{
			ID:          candidateID(m.Name(), sk.ID),
			MonitorName: m.Name(),
			Severity:    repeatSeverity(after, threshold),
			Reason: fmt.Sprintf("%d corrections on %q landed after skill was promoted (threshold %d)",
				after, k.label, threshold),
			TargetKind: TargetSkill,
			TargetID:   sk.ID,
			Evidence: []Evidence{{
				Label: "post-creation corrections",
				Content: fmt.Sprintf(
					"skill created %s; %d corrections tagged %q recorded since.",
					sk.CreatedAt.Format("2006-01-02"), after, k.label),
				Source: sk.ID,
			}},
			DetectedAt: now,
		})
	}
	return out, nil
}

// repeatSeverity scales with how far past threshold the repeat count has
// drifted. Matches the pattern used by the other monitors so the prewarm
// "⚠ high" lines mean the same thing across sources.
func repeatSeverity(count, threshold int) Severity {
	switch {
	case count >= threshold*3:
		return SeverityHigh
	case count >= threshold*2:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// firstNonStructuralTag returns the first tag that is not a known
// structural marker. Mirrors dream.correctionLabel's filter so the
// clustering stays consistent across the promotion and rumination paths.
func firstNonStructuralTag(tags []string) string {
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || isStructuralCorrectionTag(t) {
			continue
		}
		return t
	}
	return ""
}

// isStructuralCorrectionTag matches the known boilerplate tags that must
// not be used as cluster labels.
func isStructuralCorrectionTag(t string) bool {
	if t == "auto-promoted" {
		return true
	}
	if strings.HasPrefix(t, "promoted-origin:") {
		return true
	}
	if strings.HasPrefix(t, "project:") {
		return true
	}
	if strings.HasPrefix(t, "ruminated-from:") {
		return true
	}
	return false
}

// isStructuralSkillTag is the skill-side equivalent. Extra prefixes may
// appear here over time; keep it centralised.
func isStructuralSkillTag(t string) bool {
	return isStructuralCorrectionTag(t)
}

// firstWordsOfTitle returns the lowercase first n whitespace-separated
// tokens of s. Used when a correction has no tags to cluster on.
func firstWordsOfTitle(s string, n int) string {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	if len(words) == 0 {
		return ""
	}
	if len(words) > n {
		words = words[:n]
	}
	return strings.Join(words, " ")
}
