package verify

import (
	"context"
	"testing"
)

// scriptedExecutor returns canned transcripts in order. Lets tests fake
// arm-by-arm responses without invoking claude.
type scriptedExecutor struct {
	transcripts []string
	calls       []string // argv joined for assertion
	idx         int
}

func (s *scriptedExecutor) Run(_ context.Context, cmd []string) (string, error) {
	joined := ""
	for i, c := range cmd {
		if i > 0 {
			joined += " "
		}
		joined += c
	}
	s.calls = append(s.calls, joined)
	if s.idx >= len(s.transcripts) {
		return "", nil
	}
	out := s.transcripts[s.idx]
	s.idx++
	return out, nil
}

func TestEvaluateContainsAny(t *testing.T) {
	a := Assertions{ContainsAny: []string{"mnemos_session_start"}}
	if !Evaluate("called mnemos_session_start", a) {
		t.Error("should pass when transcript contains any")
	}
	if Evaluate("nothing here", a) {
		t.Error("should fail when transcript lacks all contains_any")
	}
}

func TestEvaluateLacksAll(t *testing.T) {
	a := Assertions{LacksAll: []string{"Co-Authored-By"}}
	if Evaluate("Co-Authored-By: Claude", a) {
		t.Error("should fail when forbidden substring present")
	}
	if !Evaluate("clean commit message", a) {
		t.Error("should pass when no forbidden substring")
	}
}

func TestEvaluateBothListsMustHold(t *testing.T) {
	a := Assertions{
		ContainsAny: []string{"new migration"},
		LacksAll:    []string{"edit migration 0001"},
	}
	if !Evaluate("I'll create a new migration instead", a) {
		t.Error("should pass when contains_any present and lacks_all absent")
	}
	if Evaluate("new migration but also edit migration 0001", a) {
		t.Error("forbidden substring overrides contains_any match")
	}
}

func TestRunBehaviorTalliesLift(t *testing.T) {
	// Scripted: on-arm always passes (says session_start), off-arm never does.
	// 3 runs each → on=3, off=0, lift=+1.0
	exe := &scriptedExecutor{transcripts: []string{
		"calling mnemos_session_start", "no calls",
		"calling mnemos_session_start", "no calls",
		"calling mnemos_session_start", "no calls",
	}}
	fix := &BehaviorFixture{
		Arms: BehaviorArms{
			On:  ArmCmd{Cmd: []string{"echo", "on", "{{trigger}}"}},
			Off: ArmCmd{Cmd: []string{"echo", "off", "{{trigger}}"}},
		},
		Scenarios: []BehaviorScenario{{
			Name: "s1", Trigger: "do the thing", Runs: 3,
			PassWhen: Assertions{ContainsAny: []string{"mnemos_session_start"}},
		}},
	}
	rep, err := RunBehavior(context.Background(), exe, fix)
	if err != nil {
		t.Fatalf("RunBehavior: %v", err)
	}
	got := rep.Scenarios[0]
	if got.OnPass != 3 || got.OffPass != 0 {
		t.Errorf("on=%d off=%d, want 3/0", got.OnPass, got.OffPass)
	}
	if got.Lift() != 1.0 {
		t.Errorf("lift = %v, want 1.0", got.Lift())
	}
}

func TestRunBehaviorSubstitutesTrigger(t *testing.T) {
	exe := &scriptedExecutor{transcripts: []string{"", ""}}
	fix := &BehaviorFixture{
		Arms: BehaviorArms{
			On:  ArmCmd{Cmd: []string{"sh", "-c", "echo {{trigger}}"}},
			Off: ArmCmd{Cmd: []string{"sh", "-c", "echo {{trigger}}"}},
		},
		Scenarios: []BehaviorScenario{{
			Name: "s1", Trigger: "fix the typo", Runs: 1,
			PassWhen: Assertions{ContainsAny: []string{"x"}},
		}},
	}
	if _, err := RunBehavior(context.Background(), exe, fix); err != nil {
		t.Fatal(err)
	}
	for _, c := range exe.calls {
		if !contains(c, "fix the typo") {
			t.Errorf("trigger not substituted in %q", c)
		}
	}
}

func TestRunBehaviorContextCancel(t *testing.T) {
	exe := &scriptedExecutor{transcripts: []string{""}}
	fix := &BehaviorFixture{
		Arms: BehaviorArms{
			On:  ArmCmd{Cmd: []string{"true"}},
			Off: ArmCmd{Cmd: []string{"true"}},
		},
		Scenarios: []BehaviorScenario{{
			Name: "s1", Trigger: "x", Runs: 100,
			PassWhen: Assertions{ContainsAny: []string{"x"}},
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunBehavior(ctx, exe, fix)
	if err == nil {
		t.Error("expected context error")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
