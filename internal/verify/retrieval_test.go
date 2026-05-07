package verify

import (
	"context"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// fakeSearcher returns predetermined results per query so tests assert on
// rank logic without touching storage.
type fakeSearcher struct {
	byQuery map[string][]string // query → ordered observation IDs
}

func (f fakeSearcher) Search(_ context.Context, in memory.SearchInput) ([]memory.SearchResult, error) {
	ids := f.byQuery[in.Query]
	res := make([]memory.SearchResult, 0, len(ids))
	for _, id := range ids {
		res = append(res, memory.SearchResult{Observation: memory.Observation{ID: id}})
	}
	return res, nil
}

func TestRunRetrievalPassesWhenTargetInTopK(t *testing.T) {
	fix := &RetrievalFixture{Probes: []RetrievalProbe{{
		ID:          "MEM1",
		Queries:     []string{"q1", "q2"},
		ExpectInTop: 5,
	}}}
	s := fakeSearcher{byQuery: map[string][]string{
		"q1": {"X", "Y", "MEM1", "Z"},   // rank 3
		"q2": {"A", "B", "C", "D", "E"}, // miss
	}}
	rep, err := RunRetrieval(context.Background(), s, fix)
	if err != nil {
		t.Fatalf("RunRetrieval: %v", err)
	}
	if rep.Passed != 1 {
		t.Fatalf("want 1 pass, got %d", rep.Passed)
	}
	if rep.Probes[0].BestRank != 3 {
		t.Errorf("best rank = %d, want 3", rep.Probes[0].BestRank)
	}
}

func TestRunRetrievalFailsWhenTargetBelowTopK(t *testing.T) {
	fix := &RetrievalFixture{Probes: []RetrievalProbe{{
		ID:          "MEM1",
		Queries:     []string{"q1"},
		ExpectInTop: 2,
	}}}
	s := fakeSearcher{byQuery: map[string][]string{
		"q1": {"X", "Y", "Z", "MEM1"}, // rank 4, expected ≤ 2
	}}
	rep, err := RunRetrieval(context.Background(), s, fix)
	if err != nil {
		t.Fatalf("RunRetrieval: %v", err)
	}
	if rep.Probes[0].Pass {
		t.Error("expected fail when target appears below ExpectInTop")
	}
	if rep.Probes[0].BestRank != 4 {
		t.Errorf("best rank = %d, want 4 (informative even on fail)", rep.Probes[0].BestRank)
	}
}

func TestRunRetrievalFailsWhenTargetMissing(t *testing.T) {
	fix := &RetrievalFixture{Probes: []RetrievalProbe{{
		ID: "MEM1", Queries: []string{"q1"}, ExpectInTop: 5,
	}}}
	s := fakeSearcher{byQuery: map[string][]string{"q1": {"X", "Y"}}}
	rep, err := RunRetrieval(context.Background(), s, fix)
	if err != nil {
		t.Fatalf("RunRetrieval: %v", err)
	}
	if rep.Probes[0].BestRank != 0 || rep.Probes[0].Pass {
		t.Errorf("got rank=%d pass=%v, want rank=0 pass=false",
			rep.Probes[0].BestRank, rep.Probes[0].Pass)
	}
}

func TestRunRetrievalBestAcrossQueries(t *testing.T) {
	// q2 finds it earlier than q1 — best rank should be 2, not 4.
	fix := &RetrievalFixture{Probes: []RetrievalProbe{{
		ID: "MEM1", Queries: []string{"q1", "q2"}, ExpectInTop: 5,
	}}}
	s := fakeSearcher{byQuery: map[string][]string{
		"q1": {"A", "B", "C", "MEM1"},
		"q2": {"X", "MEM1"},
	}}
	rep, _ := RunRetrieval(context.Background(), s, fix)
	if rep.Probes[0].BestRank != 2 {
		t.Errorf("best rank = %d, want 2", rep.Probes[0].BestRank)
	}
}
