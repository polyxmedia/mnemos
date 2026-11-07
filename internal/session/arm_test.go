package session_test

import (
	"fmt"
	"testing"

	"github.com/polyxmedia/mnemos/internal/session"
)

func TestAssignArm(t *testing.T) {
	cases := []struct {
		name string
		mode string
		host string
		want session.RetrievalArm
	}{
		{"push mode assigns nothing", session.ModePush, "host-1", ""},
		{"index mode assigns index", session.ModeIndex, "host-1", session.ArmIndex},
		{"index mode needs no host", session.ModeIndex, "", session.ArmIndex},
		{"experiment without host stays out", session.ModeExperiment, "", ""},
		{"unknown mode behaves as push", "typo", "host-1", ""},
		{"empty mode behaves as push", "", "host-1", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := session.AssignArm(c.mode, c.host); got != c.want {
				t.Errorf("AssignArm(%q, %q) = %q, want %q", c.mode, c.host, got, c.want)
			}
		})
	}
}

func TestAssignArmExperimentIsStableAndRoughlyEven(t *testing.T) {
	counts := map[session.RetrievalArm]int{}
	for i := 0; i < 2000; i++ {
		host := fmt.Sprintf("0192f0a1-%04d-7000-8000-000000000000", i)
		arm := session.AssignArm(session.ModeExperiment, host)
		if again := session.AssignArm(session.ModeExperiment, host); again != arm {
			t.Fatalf("host %s moved from %q to %q", host, arm, again)
		}
		counts[arm]++
	}
	if len(counts) != 2 {
		t.Fatalf("expected exactly the two arms, got %v", counts)
	}
	// Three standard deviations of a fair coin over 2000 flips is about 67.
	if push := counts[session.ArmPush]; push < 933 || push > 1067 {
		t.Errorf("split not even: %v", counts)
	}
}
