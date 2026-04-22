package rumination

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// fakeLinkReader is an in-memory LinkReader for monitor_contradiction
// tests. Filtering by link type and agent ID mirrors the real SQLite
// implementation so test assertions carry over to production semantics.
type fakeLinkReader struct {
	edges []memory.LinkEdge
	err   error
}

func (f *fakeLinkReader) ListLinks(ctx context.Context, lt memory.LinkType, agentID string, limit int) ([]memory.LinkEdge, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []memory.LinkEdge
	for _, e := range f.edges {
		if e.LinkType != "" && e.LinkType != lt {
			continue
		}
		if agentID != "" && e.TargetAgent != agentID {
			continue
		}
		out = append(out, e)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func edge(source, target, sourceTitle, targetTitle string, at time.Time) memory.LinkEdge {
	return memory.LinkEdge{
		LinkType:        memory.LinkContradicts,
		SourceID:        source,
		SourceTitle:     sourceTitle,
		SourceCreatedAt: at,
		TargetID:        target,
		TargetTitle:     targetTitle,
		TargetCreatedAt: at.Add(-24 * time.Hour),
		LinkedAt:        at,
	}
}

func TestContradictionMonitor_FiresOnSingleLink(t *testing.T) {
	when := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	reader := &fakeLinkReader{edges: []memory.LinkEdge{
		edge("src1", "tgt1", "actually we switched to pg", "use sqlite", when),
	}}
	m := &ContradictionDetectedMonitor{Links: reader, now: func() time.Time { return when }}

	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(got))
	}
	c := got[0]
	if c.TargetID != "tgt1" {
		t.Errorf("target = %s, want tgt1", c.TargetID)
	}
	if c.TargetKind != TargetObservation {
		t.Errorf("kind = %s, want observation", c.TargetKind)
	}
	if c.Severity != SeverityLow {
		t.Errorf("single link should be low severity, got %s", c.Severity)
	}
	if len(c.Evidence) != 1 {
		t.Errorf("want 1 evidence entry, got %d", len(c.Evidence))
	}
	if c.Evidence[0].Source != "src1" {
		t.Errorf("evidence source should point at the contradicting obs")
	}
}

func TestContradictionMonitor_GroupsByTarget(t *testing.T) {
	when := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	reader := &fakeLinkReader{edges: []memory.LinkEdge{
		edge("src1", "tgt1", "counter 1", "use sqlite", when),
		edge("src2", "tgt1", "counter 2", "use sqlite", when),
		edge("src3", "tgt1", "counter 3", "use sqlite", when),
		edge("src4", "tgt2", "unrelated contra", "always wrap errors", when),
	}}
	m := &ContradictionDetectedMonitor{Links: reader, now: func() time.Time { return when }}

	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 candidates (one per target), got %d", len(got))
	}

	byTarget := map[string]Candidate{}
	for _, c := range got {
		byTarget[c.TargetID] = c
	}
	if c, ok := byTarget["tgt1"]; !ok || len(c.Evidence) != 3 {
		t.Errorf("tgt1 should have 3 evidence entries, got %+v", c)
	}
	if c, ok := byTarget["tgt1"]; !ok || c.Severity != SeverityHigh {
		t.Errorf("3 contradictions should be high severity, got %s", c.Severity)
	}
	if c, ok := byTarget["tgt2"]; !ok || c.Severity != SeverityLow {
		t.Errorf("1 contradiction should be low severity, got %s", c.Severity)
	}
}

func TestContradictionMonitor_SeverityScale(t *testing.T) {
	cases := []struct {
		count    int
		severity Severity
	}{
		{1, SeverityLow},
		{2, SeverityMedium},
		{3, SeverityHigh},
		{5, SeverityHigh},
	}
	for _, tc := range cases {
		if got := contradictionSeverity(tc.count); got != tc.severity {
			t.Errorf("count=%d: got %s, want %s", tc.count, got, tc.severity)
		}
	}
}

func TestContradictionMonitor_CustomThreshold(t *testing.T) {
	// Threshold=2 means "a single contradicting source is not enough".
	// Useful when the user wants to avoid surfacing one-off challenges.
	when := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	reader := &fakeLinkReader{edges: []memory.LinkEdge{
		edge("src1", "tgt1", "lone challenger", "rule", when),
		edge("src2", "tgt2", "first of many", "other rule", when),
		edge("src3", "tgt2", "second", "other rule", when),
	}}
	m := &ContradictionDetectedMonitor{Links: reader, Threshold: 2}
	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 candidate (threshold=2), got %d", len(got))
	}
	if got[0].TargetID != "tgt2" {
		t.Errorf("should have emitted tgt2, got %s", got[0].TargetID)
	}
}

func TestContradictionMonitor_NoLinksSilent(t *testing.T) {
	m := &ContradictionDetectedMonitor{Links: &fakeLinkReader{}}
	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty link store should fire no candidates, got %+v", got)
	}
}

func TestContradictionMonitor_ErrorPropagates(t *testing.T) {
	want := errors.New("db exploded")
	m := &ContradictionDetectedMonitor{Links: &fakeLinkReader{err: want}}
	_, err := m.Detect(context.Background())
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want wrapping of %v", err, want)
	}
}
