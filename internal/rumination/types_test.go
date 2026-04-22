package rumination

import "testing"

func TestSeverity_String(t *testing.T) {
	cases := map[Severity]string{
		SeverityLow:    "low",
		SeverityMedium: "medium",
		SeverityHigh:   "high",
		Severity(0):    "unknown",
		Severity(42):   "unknown",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", s, got, want)
		}
	}
}
