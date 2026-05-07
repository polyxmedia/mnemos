package verify

import (
	"context"
	"fmt"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// Searcher is the narrow surface RunRetrieval needs from memory.Service.
// Declared here at the consumer side so tests can fake it without booting
// the storage layer.
type Searcher interface {
	Search(ctx context.Context, in memory.SearchInput) ([]memory.SearchResult, error)
}

// RetrievalReport summarises a retrieval probe run.
type RetrievalReport struct {
	Probes []ProbeOutcome
	Total  int
	Passed int
}

// ProbeOutcome is the per-probe result. BestRank is the smallest rank
// (1-indexed) at which the target ID appeared across all queries; 0 means
// the probe never surfaced the target. Pass is BestRank in [1, ExpectInTop].
type ProbeOutcome struct {
	Probe    RetrievalProbe
	Queries  []QueryHit
	BestRank int
	Pass     bool
}

// QueryHit records what a single query produced for a probe.
type QueryHit struct {
	Query string
	Rank  int // 1-indexed; 0 means target not in returned results
}

// RunRetrieval executes every probe in the fixture against the searcher and
// returns a structured report. Pulls 3x ExpectInTop hits per query so the
// report shows near-misses (target found at rank 8 when expected ≤ 5).
func RunRetrieval(ctx context.Context, s Searcher, fix *RetrievalFixture) (*RetrievalReport, error) {
	if fix == nil {
		return nil, fmt.Errorf("nil fixture")
	}
	rep := &RetrievalReport{Total: len(fix.Probes)}
	for _, p := range fix.Probes {
		out := ProbeOutcome{Probe: p}
		limit := p.ExpectInTop * 3
		if limit < 10 {
			limit = 10
		}
		best := 0
		for _, q := range p.Queries {
			res, err := s.Search(ctx, memory.SearchInput{
				Query:   q,
				Project: p.Project,
				Limit:   limit,
			})
			if err != nil {
				return nil, fmt.Errorf("search %q: %w", q, err)
			}
			rank := 0
			for i, r := range res {
				if r.Observation.ID == p.ID {
					rank = i + 1
					break
				}
			}
			out.Queries = append(out.Queries, QueryHit{Query: q, Rank: rank})
			if rank > 0 && (best == 0 || rank < best) {
				best = rank
			}
		}
		out.BestRank = best
		out.Pass = best > 0 && best <= p.ExpectInTop
		if out.Pass {
			rep.Passed++
		}
		rep.Probes = append(rep.Probes, out)
	}
	return rep, nil
}
