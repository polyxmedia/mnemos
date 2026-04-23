package rumination

import (
	"context"
	"fmt"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// StaleRawObservationMonitor flags raw-tier observations that have been
// sitting in quarantine too long without being promoted or dismissed.
// Raw is the "recorded, not yet validated" tier — tool output, agent
// inference, imports from untrusted sources. If an observation sits raw
// for weeks it is either (a) forgotten and rotting silently, or (b) not
// worth keeping. Either outcome is a net positive to resolve.
//
// Defaults: StaleDays = 14, MaxPerRun = 50. Fourteen days is the
// "probably not coming back to confirm it" threshold — aggressive enough
// to keep quarantine from filling forever, conservative enough that a
// busy week of tool-fetched context won't get swept up prematurely.
type StaleRawObservationMonitor struct {
	Observations RawObservationReader
	AgentID      string
	StaleDays    int
	MaxPerRun    int

	now func() time.Time // test override
}

// RawObservationReader is the narrow read surface the monitor depends on.
// Declared at the consumer (Go idiom) so tests supply a tight fake and
// the real storage implementation satisfies it implicitly via Reader.
type RawObservationReader interface {
	ListByTrustTier(ctx context.Context, agentID string, tier memory.TrustTier, limit int) ([]memory.Observation, error)
}

// Name implements Monitor.
func (m *StaleRawObservationMonitor) Name() string { return "stale-raw" }

// Detect returns one Candidate per stale raw observation, ordered oldest
// first by the underlying query. The severity scales with how far past
// the threshold the observation has drifted — the same ratio pattern as
// the stale-skill monitor so "severity high" has consistent meaning.
func (m *StaleRawObservationMonitor) Detect(ctx context.Context) ([]Candidate, error) {
	staleDays := m.StaleDays
	if staleDays <= 0 {
		staleDays = 14
	}
	limit := m.MaxPerRun
	if limit <= 0 {
		limit = 50
	}
	clock := m.now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	now := clock()
	cutoff := now.AddDate(0, 0, -staleDays)

	list, err := m.Observations.ListByTrustTier(ctx, m.AgentID, memory.TrustRaw, limit)
	if err != nil {
		return nil, fmt.Errorf("list raw: %w", err)
	}

	out := make([]Candidate, 0, len(list))
	for _, o := range list {
		if !o.CreatedAt.Before(cutoff) {
			continue
		}
		ageDays := int(now.Sub(o.CreatedAt).Hours() / 24)
		out = append(out, Candidate{
			ID:          candidateID(m.Name(), o.ID),
			MonitorName: m.Name(),
			Severity:    staleRawSeverity(ageDays, staleDays),
			Reason: fmt.Sprintf("raw-tier observation unpromoted %d days (source_kind=%s)",
				ageDays, o.SourceKind),
			TargetKind: TargetObservation,
			TargetID:   o.ID,
			Evidence: []Evidence{{
				Label:   "quarantine history",
				Content: fmt.Sprintf("created %s; source=%s; no promotion to curated or skill yet.", o.CreatedAt.Format("2006-01-02"), o.SourceKind),
				Source:  o.ID,
			}},
			DetectedAt: now,
		})
	}
	return out, nil
}

// staleRawSeverity mirrors the skill-stale monitor's shape. A raw row at
// 2× the stale threshold is medium, 3× is high. Base is low — the tier
// was never promised to be permanent, it was just never re-examined.
func staleRawSeverity(ageDays, cutoffDays int) Severity {
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
