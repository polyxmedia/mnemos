package rumination

import (
	"context"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// fakeRawReader is the minimal RawObservationReader satisfying the
// StaleRawObservationMonitor's narrow interface. Declared at the test
// site (consumer-owned pattern) so we do not depend on the full
// memory.Reader just to exercise one method.
type fakeRawReader struct {
	items []memory.Observation
	err   error
}

func (f *fakeRawReader) ListByTrustTier(ctx context.Context, agentID string, tier memory.TrustTier, limit int) ([]memory.Observation, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]memory.Observation, 0, len(f.items))
	for _, o := range f.items {
		if tier != "" && o.TrustTier != tier {
			continue
		}
		out = append(out, o)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func TestStaleRawMonitorFiresOnOldRaw(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	reader := &fakeRawReader{items: []memory.Observation{
		// 30 days old = 2.1× the default 14-day threshold → medium.
		{ID: "old-raw", TrustTier: memory.TrustRaw, SourceKind: memory.SourceTool,
			CreatedAt: now.AddDate(0, 0, -30)},
		// Below cutoff — should not fire.
		{ID: "fresh", TrustTier: memory.TrustRaw, SourceKind: memory.SourceTool,
			CreatedAt: now.AddDate(0, 0, -3)},
		// 45 days old → 3.2× threshold → high.
		{ID: "ancient", TrustTier: memory.TrustRaw, SourceKind: memory.SourceAgentInference,
			CreatedAt: now.AddDate(0, 0, -45)},
	}}
	m := &StaleRawObservationMonitor{
		Observations: reader,
		StaleDays:    14,
		now:          func() time.Time { return now },
	}

	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 candidates (old-raw + ancient), got %d: %v", len(got), got)
	}

	sev := map[string]Severity{}
	for _, c := range got {
		sev[c.TargetID] = c.Severity
	}
	if sev["old-raw"] != SeverityMedium {
		t.Errorf("30-day raw should be medium, got %v", sev["old-raw"])
	}
	if sev["ancient"] != SeverityHigh {
		t.Errorf("45-day raw should be high, got %v", sev["ancient"])
	}
}

func TestStaleRawMonitorSkipsCurated(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	reader := &fakeRawReader{items: []memory.Observation{
		{ID: "curated", TrustTier: memory.TrustCurated, CreatedAt: now.AddDate(0, -6, 0)},
		{ID: "raw", TrustTier: memory.TrustRaw, CreatedAt: now.AddDate(0, -6, 0)},
	}}
	m := &StaleRawObservationMonitor{
		Observations: reader,
		now:          func() time.Time { return now },
	}

	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(got) != 1 || got[0].TargetID != "raw" {
		t.Errorf("monitor should fire only on raw-tier rows, got %+v", got)
	}
}

func TestStaleRawSeverityRatios(t *testing.T) {
	cases := []struct {
		age, cutoff int
		want        Severity
	}{
		{15, 14, SeverityLow},
		{28, 14, SeverityMedium},
		{42, 14, SeverityHigh},
		{100, 14, SeverityHigh},
		{5, 0, SeverityLow},
	}
	for _, c := range cases {
		if got := staleRawSeverity(c.age, c.cutoff); got != c.want {
			t.Errorf("staleRawSeverity(%d, %d) = %v, want %v", c.age, c.cutoff, got, c.want)
		}
	}
}

func TestStaleRawMonitorName(t *testing.T) {
	m := &StaleRawObservationMonitor{}
	if m.Name() != "stale-raw" {
		t.Errorf("monitor name drift: got %q", m.Name())
	}
}
