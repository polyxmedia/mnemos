package verify

import (
	"context"
	"testing"
)

func TestRunCaptureCountsToolCalls(t *testing.T) {
	// Three runs: first two have a correct tool_use, third doesn't. Rate = 2/3.
	exe := &scriptedExecutor{transcripts: []string{
		`{"type":"tool_use","name":"mcp__mnemos__mnemos_correct"}`,
		`{"type":"tool_use","name":"mcp__mnemos__mnemos_correct"}`,
		`{"type":"tool_use","name":"Bash"}`,
	}}
	fix := &CaptureFixture{
		Arm: ArmCmd{Cmd: []string{"echo", "{{trigger}}"}},
		Scenarios: []CaptureScenario{{
			Name: "correction", Trigger: "x", Runs: 3,
			ExpectTools: []string{"mcp__mnemos__mnemos_correct", "mcp__mnemos__mnemos_save"},
		}},
	}
	rep, err := RunCapture(context.Background(), exe, fix)
	if err != nil {
		t.Fatalf("RunCapture: %v", err)
	}
	got := rep.Scenarios[0]
	if got.Captured != 2 {
		t.Errorf("captured = %d, want 2", got.Captured)
	}
	if got.Rate() != 2.0/3.0 {
		t.Errorf("rate = %v, want %v", got.Rate(), 2.0/3.0)
	}
}

func TestRunCaptureIgnoresSystemInitToolList(t *testing.T) {
	// The system-init message lists every tool name as a string in the
	// "tools" array. Bare-name matching would falsely pass; we want only
	// real tool_use entries to count.
	systemInit := `{"type":"system","tools":["mcp__mnemos__mnemos_correct","Bash"]}`
	exe := &scriptedExecutor{transcripts: []string{systemInit}}
	fix := &CaptureFixture{
		Arm: ArmCmd{Cmd: []string{"echo"}},
		Scenarios: []CaptureScenario{{
			Name: "init-only", Trigger: "x", Runs: 1,
			ExpectTools: []string{"mcp__mnemos__mnemos_correct"},
		}},
	}
	rep, _ := RunCapture(context.Background(), exe, fix)
	if rep.Scenarios[0].Captured != 0 {
		t.Errorf("system-init must not count as capture, got %d", rep.Scenarios[0].Captured)
	}
}

func TestRunCaptureRequiresArmCmd(t *testing.T) {
	fix := &CaptureFixture{Scenarios: []CaptureScenario{{Name: "x", Runs: 1}}}
	if _, err := RunCapture(context.Background(), &scriptedExecutor{}, fix); err == nil {
		t.Error("expected error when arm.cmd missing")
	}
}

func TestRunCaptureContextCancel(t *testing.T) {
	exe := &scriptedExecutor{}
	fix := &CaptureFixture{
		Arm: ArmCmd{Cmd: []string{"true"}},
		Scenarios: []CaptureScenario{{
			Name: "x", Trigger: "y", Runs: 100,
			ExpectTools: []string{"mcp__mnemos__mnemos_save"},
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunCapture(ctx, exe, fix); err == nil {
		t.Error("expected context error")
	}
}
