package rumination

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// ContradictionDetectedMonitor flags live observations (typically
// conventions or decisions) that are the target of one or more
// `contradicts` links from other live observations. Supersedes already
// invalidates the target automatically; contradicts is the weaker link
// that leaves the tension unresolved — which is exactly the case
// rumination exists to close.
//
// Link-based detection is LLM-free and deterministic: the signal comes
// from agents (or users) who explicitly flagged the contradiction via
// mnemos_link. This keeps the memory-layer-LLM-free invariant while
// still surfacing conventions under real disagreement.
//
// Implementation note: unlike the skill-scoped monitors, this one is
// cross-project — a convention in one project can be contradicted from
// any other. Callers who need project scoping should filter the emitted
// candidates downstream, or add a project filter to LinkReader later.
type ContradictionDetectedMonitor struct {
	Links   LinkReader
	AgentID string

	// Threshold is the minimum number of distinct contradicting sources
	// required before the monitor fires. Defaults to 1: any contradiction
	// link is enough, because the agent already committed to the flag.
	Threshold int

	// MaxEdges caps the link-list query size. Defaults to 1000.
	MaxEdges int

	now func() time.Time
}

// LinkReader is the narrow link-graph read surface the monitor depends
// on. Declared at the consumer so tests supply a minimal fake and the
// storage implementation satisfies it implicitly via memory.Reader.
type LinkReader interface {
	ListLinks(ctx context.Context, linkType memory.LinkType, agentID string, limit int) ([]memory.LinkEdge, error)
}

// Name implements Monitor.
func (m *ContradictionDetectedMonitor) Name() string {
	return "contradiction-detected"
}

// Detect groups live contradicts edges by target and emits one Candidate
// per target whose inbound-contradiction count meets Threshold. Each
// candidate carries the contradicting sources as evidence so the packaged
// review block shows the agent exactly what to respond to.
func (m *ContradictionDetectedMonitor) Detect(ctx context.Context) ([]Candidate, error) {
	threshold := m.Threshold
	if threshold <= 0 {
		threshold = 1
	}
	limit := m.MaxEdges
	if limit <= 0 {
		limit = 1000
	}
	clock := m.now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	now := clock()

	edges, err := m.Links.ListLinks(ctx, memory.LinkContradicts, m.AgentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list contradict links: %w", err)
	}

	// Group by target. Keep insertion order of first sight so tests are
	// stable and severe cases (high fan-in) naturally surface at top after
	// the Service's severity sort.
	type bucket struct {
		target memory.LinkEdge
		edges  []memory.LinkEdge
	}
	order := make([]string, 0)
	byTarget := make(map[string]*bucket)
	for _, e := range edges {
		b, ok := byTarget[e.TargetID]
		if !ok {
			b = &bucket{target: e}
			byTarget[e.TargetID] = b
			order = append(order, e.TargetID)
		}
		b.edges = append(b.edges, e)
	}

	// Deterministic output: sort target IDs so two runs over the same
	// store produce the same order. Matches the pattern from promote.go.
	sort.Strings(order)

	out := make([]Candidate, 0)
	for _, tid := range order {
		b := byTarget[tid]
		if len(b.edges) < threshold {
			continue
		}
		// Provenance weighting (Bet 2 phase 3): curated > raw. Raw-tier
		// contradictors come from unvalidated sources (tool output, agent
		// inference), so a target contradicted only by raw evidence is a
		// weaker signal than one contradicted by curated peers. The count
		// for severity purposes is curated edges + raw edges discounted
		// by half, floored at 1 per edge.
		curatedCount, rawCount := 0, 0
		evidence := make([]Evidence, 0, len(b.edges))
		for _, e := range b.edges {
			tierLabel := string(e.SourceTrustTier)
			if tierLabel == "" {
				tierLabel = string(memory.TrustCurated)
			}
			if memory.TrustTier(tierLabel) == memory.TrustRaw {
				rawCount++
			} else {
				curatedCount++
			}
			evidence = append(evidence, Evidence{
				Label: "contradicted by",
				Content: fmt.Sprintf("%q (%s tier, recorded %s)",
					e.SourceTitle, tierLabel, e.SourceCreatedAt.Format("2006-01-02")),
				Source: e.SourceID,
			})
		}
		sev := contradictionSeverityWeighted(curatedCount, rawCount)
		reason := fmt.Sprintf("%d live contradiction(s) against %q",
			len(b.edges), b.target.TargetTitle)
		if rawCount > 0 && curatedCount == 0 {
			reason += " (all raw-tier — low-confidence evidence)"
		} else if rawCount > 0 {
			reason += fmt.Sprintf(" (%d curated, %d raw)", curatedCount, rawCount)
		}
		out = append(out, Candidate{
			ID:          candidateID(m.Name(), tid),
			MonitorName: m.Name(),
			Severity:    sev,
			Reason:      reason,
			TargetKind:  TargetObservation,
			TargetID:    tid,
			Evidence:    evidence,
			DetectedAt:  now,
		})
	}
	return out, nil
}

// contradictionSeverity buckets by how many distinct sources challenge
// the target. One source is the floor (low); 3+ signals a real pile-on
// (high). The jumps match the other monitors' cadence so agents seeing
// "severity high" across the UI mean the same thing everywhere.
func contradictionSeverity(count int) Severity {
	switch {
	case count >= 3:
		return SeverityHigh
	case count >= 2:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// contradictionSeverityWeighted applies the Bet 2 phase 3 rule: curated
// contradictors count in full, raw ones count half (rounded down), floored
// at 1 when any contradictor exists. A target contradicted only by two
// raw-tier observations becomes medium (2×0.5=1), never high; a target
// with one curated + two raw becomes 2 (medium). Keeps the public
// contradictionSeverity function pure in case anyone still wants the
// unweighted version in a test or alternate flow.
func contradictionSeverityWeighted(curatedCount, rawCount int) Severity {
	weighted := curatedCount + rawCount/2
	if weighted < 1 && (curatedCount+rawCount) > 0 {
		weighted = 1
	}
	return contradictionSeverity(weighted)
}
