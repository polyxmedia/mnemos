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

func edgeWithTier(source, target, sourceTitle, targetTitle string, at time.Time, srcTier memory.TrustTier) memory.LinkEdge {
	e := edge(source, target, sourceTitle, targetTitle, at)
	e.SourceTrustTier = srcTier
	return e
}

func TestContradictionMonitor_DowngradesRawOnlyEvidence(t *testing.T) {
	when := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	// Two raw-tier contradictors. Unweighted count would be 2 (medium),
	// but both are raw so weighted count is 2*0.5=1 → low.
	reader := &fakeLinkReader{edges: []memory.LinkEdge{
		edgeWithTier("raw1", "tgt", "maybe switch", "use sqlite", when, memory.TrustRaw),
		edgeWithTier("raw2", "tgt", "also saw elsewhere", "use sqlite", when, memory.TrustRaw),
	}}
	m := &ContradictionDetectedMonitor{Links: reader, now: func() time.Time { return when }}

	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(got))
	}
	if got[0].Severity != SeverityLow {
		t.Errorf("raw-only contradictors should weight down to low, got %v", got[0].Severity)
	}
	if got[0].Reason == "" || !stringContains(got[0].Reason, "all raw-tier") {
		t.Errorf("reason should note raw-only provenance, got %q", got[0].Reason)
	}
}

func TestContradictionMonitor_MixedTierReasonNotesSplit(t *testing.T) {
	when := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	// Two curated + one raw = weighted count 2 + 0 (raw/2 rounds down) = 2 (medium).
	reader := &fakeLinkReader{edges: []memory.LinkEdge{
		edgeWithTier("cur1", "tgt", "definitely wrong", "use sqlite", when, memory.TrustCurated),
		edgeWithTier("cur2", "tgt", "also definitely wrong", "use sqlite", when, memory.TrustCurated),
		edgeWithTier("raw1", "tgt", "some blog said", "use sqlite", when, memory.TrustRaw),
	}}
	m := &ContradictionDetectedMonitor{Links: reader, now: func() time.Time { return when }}
	got, _ := m.Detect(context.Background())
	if len(got) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(got))
	}
	if got[0].Severity != SeverityMedium {
		t.Errorf("2 curated + 1 raw should weight to medium, got %v", got[0].Severity)
	}
	if !stringContains(got[0].Reason, "2 curated, 1 raw") {
		t.Errorf("reason should split curated/raw counts, got %q", got[0].Reason)
	}
}

func TestContradictionSeverityWeighted(t *testing.T) {
	cases := []struct {
		curated, raw int
		want         Severity
	}{
		{1, 0, SeverityLow},
		{2, 0, SeverityMedium},
		{3, 0, SeverityHigh},
		{0, 1, SeverityLow},    // single raw floors at 1 → low
		{0, 2, SeverityLow},    // 2 raw → weighted 1 → low
		{0, 4, SeverityMedium}, // 4 raw → weighted 2 → medium
		{0, 6, SeverityHigh},   // 6 raw → weighted 3 → high
		{1, 2, SeverityMedium}, // 1 + 1 = 2 → medium
	}
	for _, c := range cases {
		if got := contradictionSeverityWeighted(c.curated, c.raw); got != c.want {
			t.Errorf("weighted(%d curated, %d raw) = %v, want %v", c.curated, c.raw, got, c.want)
		}
	}
}

// stringContains is a test helper that spells out strings.Contains so the
// test imports list stays minimal.
func stringContains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
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
