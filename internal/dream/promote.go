package dream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// correctionData is the structured payload every correction carries. Mirrors
// what mnemos_correct writes into memory.SaveInput.Structured.
type correctionData struct {
	Tried          string `json:"tried"`
	WrongBecause   string `json:"wrong_because"`
	Fix            string `json:"fix"`
	TriggerContext string `json:"trigger_context"`
}

// promotionDefaults
const (
	// minCorrectionsPerGroup is the floor that proves a pattern rather than
	// a one-off. Three is the smallest N where the mean is robust to noise
	// — two is a coincidence, three is a trend.
	minCorrectionsPerGroup = 3

	// tagPromoted marks skills produced by this pipeline. Makes them easy
	// to filter (`mnemos skill list --promoted`) and recognise in stats.
	tagPromoted = "auto-promoted"

	// originPrefix is the tag prefix for the stable group hash. Used as
	// the idempotency key: promoting again finds the same skill by this
	// tag and bumps its version rather than creating duplicates.
	originPrefix = "promoted-origin:"
)

// promoteSkillsFromCorrections scans live correction observations across
// all projects, clusters them by (agent_id, project, label), and when a
// cluster reaches minCorrectionsPerGroup synthesises a skill. Returns the
// count of skills created or version-bumped.
//
// Labels:
//   - When a correction has tags, the first tag is the label (projects
//     naturally tag oauth/retry/serialisation corrections with matching
//     tokens).
//   - Otherwise the label is the first three words of the title, normalised.
//
// Idempotency: each group carries a stable hash (sha256 of
// agent_id|project|label, truncated). Promotion writes that hash as a
// skill tag `promoted-origin:<hash>`. A later pass finds the existing skill
// by tag and upserts — version bumps, source_sessions extend, no dupes.
func (s *Service) promoteSkillsFromCorrections(ctx context.Context) (int, error) {
	if s.skills == nil || s.reader == nil {
		return 0, nil
	}
	// A single bulk fetch; corrections are rarer than generic observations
	// so the limit is forgiving. If a user ever exceeds it in practice we
	// can add pagination, but 10k in-memory is cheap.
	corrections, err := s.reader.ListByProject(ctx, "", "", memory.TypeCorrection, 10000)
	if err != nil {
		return 0, fmt.Errorf("list corrections: %w", err)
	}
	groups := groupCorrections(corrections)

	n := 0
	for _, g := range groups {
		if len(g.corrections) < minCorrectionsPerGroup {
			continue
		}
		ok, err := s.upsertPromotedSkill(ctx, g)
		if err != nil {
			s.log.Warn("promote skill", "err", err, "label", g.label, "project", g.project)
			continue
		}
		if ok {
			n++
		}
	}
	return n, nil
}

// correctionGroup is an in-memory cluster of correction observations that
// share the same (agent_id, project, label). The hash is the idempotency
// key carried on the produced skill's tags.
type correctionGroup struct {
	agentID     string
	project     string
	label       string
	hash        string
	corrections []memory.Observation
}

// groupCorrections clusters by (agent_id, project, label). Order matters
// for deterministic output in tests: we sort keys alphabetically before
// building the result slice so the same input always produces the same
// iteration order.
func groupCorrections(obs []memory.Observation) []correctionGroup {
	// Use a keyed map while building, then flatten in deterministic order.
	type key struct{ agent, project, label string }
	byKey := make(map[key]*correctionGroup)
	for _, o := range obs {
		label := correctionLabel(o)
		if label == "" {
			continue // skip un-groupable corrections silently
		}
		k := key{agent: defaultAgent(o.AgentID), project: o.Project, label: label}
		g, ok := byKey[k]
		if !ok {
			g = &correctionGroup{
				agentID: k.agent, project: k.project, label: k.label,
				hash: groupHash(k.agent, k.project, k.label),
			}
			byKey[k] = g
		}
		g.corrections = append(g.corrections, o)
	}
	keys := make([]key, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].project != keys[j].project {
			return keys[i].project < keys[j].project
		}
		return keys[i].label < keys[j].label
	})
	out := make([]correctionGroup, 0, len(keys))
	for _, k := range keys {
		out = append(out, *byKey[k])
	}
	return out
}

// correctionLabel picks the clustering label for one correction: first tag
// if present, otherwise the first three whitespace-separated tokens of the
// title, lowercased. Empty titles yield "" (caller skips).
func correctionLabel(o memory.Observation) string {
	for _, t := range o.Tags {
		t = strings.TrimSpace(t)
		// Skip purely structural tags so the grouping key reflects the
		// subject matter rather than boilerplate.
		if t == "" || t == tagPromoted || strings.HasPrefix(t, originPrefix) {
			continue
		}
		return strings.ToLower(t)
	}
	words := strings.Fields(strings.ToLower(strings.TrimSpace(o.Title)))
	if len(words) == 0 {
		return ""
	}
	if len(words) > 3 {
		words = words[:3]
	}
	return strings.Join(words, " ")
}

func groupHash(agentID, project, label string) string {
	sum := sha256.Sum256([]byte(agentID + "|" + project + "|" + label))
	return hex.EncodeToString(sum[:])[:12]
}

func defaultAgent(id string) string {
	if id == "" {
		return "default"
	}
	return id
}

// upsertPromotedSkill synthesises or version-bumps a skill for the given
// group. Returns true when the skill was created or updated (version went
// up), false when the existing skill already matched exactly.
func (s *Service) upsertPromotedSkill(ctx context.Context, g correctionGroup) (bool, error) {
	procedure, pitfalls := synthesisePromotion(g)

	existing, err := s.findSkillByOrigin(ctx, g.agentID, g.hash)
	if err != nil {
		return false, err
	}

	sources := sessionIDs(g.corrections)
	description := fmt.Sprintf("Auto-promoted from %d corrections in %s: %s",
		len(g.corrections), g.project, g.label)
	name := promotedSkillName(g)

	tags := []string{
		tagPromoted,
		originPrefix + g.hash,
		"project:" + g.project,
	}

	if existing != nil {
		// A pass with no new corrections since last run is a no-op — skip
		// the upsert so we don't bump the version needlessly.
		if sameSourceSet(existing.SourceSessions, sources) &&
			existing.Procedure == procedure &&
			existing.Pitfalls == pitfalls {
			return false, nil
		}
		// Preserve the original name even if the label has shifted — once
		// a skill is out in the world, its identity is stable.
		name = existing.Name
	}

	_, err = s.skills.Save(ctx, skills.SaveInput{
		AgentID:        g.agentID,
		Name:           name,
		Description:    description,
		Procedure:      procedure,
		Pitfalls:       pitfalls,
		Tags:           tags,
		SourceSessions: sources,
	})
	if err != nil {
		return false, fmt.Errorf("save skill: %w", err)
	}
	return true, nil
}

// promotedSkillName is derived once, at first synthesis. Keeping it in one
// place makes the naming convention discoverable.
func promotedSkillName(g correctionGroup) string {
	return fmt.Sprintf("auto: %s (%s)", g.label, g.project)
}

// synthesisePromotion renders the procedure and pitfalls text from a
// group's correction observations. Each correction contributes one
// "avoid → do" pair. Triggers are collected once (deduped) to anchor the
// skill to the situations it applies to.
func synthesisePromotion(g correctionGroup) (procedure, pitfalls string) {
	triggers := map[string]bool{}
	var avoids, fixes []string
	var pitfallLines []string

	for _, o := range g.corrections {
		c := decodeCorrection(o)
		if c.TriggerContext != "" {
			triggers[strings.TrimSpace(c.TriggerContext)] = true
		}
		if c.Tried != "" && c.WrongBecause != "" {
			avoids = append(avoids, fmt.Sprintf("- %s — %s", c.Tried, c.WrongBecause))
		}
		if c.Fix != "" {
			fixes = append(fixes, "- "+c.Fix)
		}
		if c.WrongBecause != "" {
			pitfallLines = append(pitfallLines, "- "+c.WrongBecause)
		}
	}

	var b strings.Builder
	if len(triggers) > 0 {
		fmt.Fprintln(&b, "## When this applies")
		for _, t := range sortedKeys(triggers) {
			fmt.Fprintln(&b, "- "+t)
		}
		fmt.Fprintln(&b)
	}
	if len(avoids) > 0 {
		fmt.Fprintln(&b, "## Avoid")
		for _, a := range dedupeStable(avoids) {
			fmt.Fprintln(&b, a)
		}
		fmt.Fprintln(&b)
	}
	if len(fixes) > 0 {
		fmt.Fprintln(&b, "## Do")
		for _, f := range dedupeStable(fixes) {
			fmt.Fprintln(&b, f)
		}
	}

	procedure = strings.TrimRight(b.String(), "\n")
	pitfalls = strings.TrimRight(strings.Join(dedupeStable(pitfallLines), "\n"), "\n")
	return procedure, pitfalls
}

// decodeCorrection pulls the structured payload. Returns zero values if
// the observation has no structured JSON (should not happen for type=
// correction but we stay defensive).
func decodeCorrection(o memory.Observation) correctionData {
	var c correctionData
	if o.Structured == "" {
		return c
	}
	_ = json.Unmarshal([]byte(o.Structured), &c)
	return c
}

// findSkillByOrigin looks up an existing skill for this agent tagged with
// originPrefix+hash. Skills are modest in volume so a client-side scan is
// fine; a dedicated index would be premature.
func (s *Service) findSkillByOrigin(ctx context.Context, agentID, hash string) (*skills.Skill, error) {
	want := originPrefix + hash
	list, err := s.skills.List(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	for i := range list {
		for _, t := range list[i].Tags {
			if t == want {
				return &list[i], nil
			}
		}
	}
	return nil, nil
}

// sessionIDs returns the union of distinct session_ids contributing to the
// group, in insertion order. Used as provenance on the promoted skill.
func sessionIDs(obs []memory.Observation) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(obs))
	for _, o := range obs {
		if o.SessionID == "" || seen[o.SessionID] {
			continue
		}
		seen[o.SessionID] = true
		out = append(out, o.SessionID)
	}
	return out
}

// sameSourceSet reports whether two source-session lists represent the
// same set, ignoring order. Stringified skill source lists come out of
// JSON so we compare by set membership not sequence.
func sameSourceSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		if !set[x] {
			return false
		}
	}
	return true
}

func dedupeStable(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
