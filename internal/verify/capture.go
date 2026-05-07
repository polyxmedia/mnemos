package verify

import (
	"context"
	"fmt"
)

// CaptureFixture exercises the WRITE side of mnemos: do agents record
// corrections, conventions, and decisions that the user hands them? Single
// arm by design — the off-arm has no save/correct/convention tools so the
// comparison is uninformative; the metric that matters is the on-arm rate.
type CaptureFixture struct {
	Arm       ArmCmd            `yaml:"arm"`
	Scenarios []CaptureScenario `yaml:"scenarios"`
}

// CaptureScenario embeds a user correction (or convention, or decision) in
// a trigger prompt and asserts that at least one of ExpectTools fired in
// the resulting transcript. Mix scenarios across explicitness levels: an
// explicit "save this" should pass nearly always; a quiet "btw we always X"
// is the realistic stress test.
type CaptureScenario struct {
	Name        string   `yaml:"name"`
	Trigger     string   `yaml:"trigger"`
	ExpectTools []string `yaml:"expect_tools"`
	Runs        int      `yaml:"runs"`
}

// CaptureReport aggregates per-scenario capture rates.
type CaptureReport struct {
	Scenarios []CaptureOutcome
}

// CaptureOutcome is the per-scenario result. Captured is the number of
// runs in which at least one expected tool fired; Runs is the sample size.
type CaptureOutcome struct {
	Scenario CaptureScenario
	Captured int
	Runs     int
	Errors   []string
}

// Rate is captured / runs as a fraction in [0, 1].
func (c CaptureOutcome) Rate() float64 {
	if c.Runs == 0 {
		return 0
	}
	return float64(c.Captured) / float64(c.Runs)
}

// RunCapture executes each scenario the configured number of times and
// asserts that one of ExpectTools appears as a tool_use in the transcript.
// Substring match looks for the precise `"name":"<tool>"` shape so the
// system-init available-tools listing doesn't trivially pass the assertion
// (same trick as the behavior harness).
func RunCapture(ctx context.Context, exe Executor, fix *CaptureFixture) (*CaptureReport, error) {
	if fix == nil {
		return nil, fmt.Errorf("nil fixture")
	}
	if len(fix.Arm.Cmd) == 0 {
		return nil, fmt.Errorf("arm.cmd is required")
	}
	rep := &CaptureReport{}
	for _, sc := range fix.Scenarios {
		out := CaptureOutcome{Scenario: sc, Runs: sc.Runs}
		for i := 0; i < sc.Runs; i++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			cmd := substitute(fix.Arm.Cmd, sc.Trigger)
			tr, runErr := exe.Run(ctx, cmd)
			if runErr != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("run %d: %v", i+1, runErr))
			}
			if hasAnyToolCall(tr, sc.ExpectTools) {
				out.Captured++
			}
		}
		rep.Scenarios = append(rep.Scenarios, out)
	}
	return rep, nil
}

// hasAnyToolCall reports whether the transcript contains a tool_use entry
// for any tool in want. Builds the precise `"name":"<tool>"` substring
// rather than matching bare tool names, which would also match the
// system-init available-tools listing and cause every on-arm run to
// trivially pass.
func hasAnyToolCall(transcript string, want []string) bool {
	for _, t := range want {
		needle := `"name":"` + t + `"`
		if containsSubstring(transcript, needle) {
			return true
		}
	}
	return false
}

// containsSubstring is strings.Contains rewritten so capture.go avoids
// importing strings just for one call. (Behavior.go already pulls strings
// in for substitute()/Evaluate().)
func containsSubstring(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
