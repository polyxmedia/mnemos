package rumination

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPack_RendersAllSections(t *testing.T) {
	c := Candidate{
		ID:          "rumination-abc123456789",
		MonitorName: "skill-effectiveness-floor",
		Severity:    SeverityHigh,
		Reason:      "effectiveness 0.10 after 20 uses (floor 0.30)",
		TargetKind:  TargetSkill,
		TargetID:    "sk1",
		Evidence: []Evidence{
			{Label: "effectiveness history", Content: "20 uses recorded, 2 succeeded (0.10).", Source: "sk1"},
			{Label: "recent correction", Content: "user had to override the rule again on 2026-04-18", Source: "obs42"},
		},
	}
	target := TargetRef{
		Kind: TargetSkill,
		ID:   "sk1",
		Name: "retry on 401",
		Body: "On a 401 response, retry the request immediately.",
	}

	block := Pack(c, target)

	wantSections := []string{
		"# Rumination · retry on 401",
		"_Triggered by **skill-effectiveness-floor** (severity high): effectiveness 0.10 after 20 uses (floor 0.30)_",
		"## Hypothesis under review",
		"On a 401 response, retry the request immediately.",
		"## Disconfirming evidence",
		"- **effectiveness history**",
		"- **recent correction**",
		"## Falsifiable restatement",
		"## Hostile review — answer before proposing a revision",
		"## Action",
		"mnemos_skill_save",
		"ruminated-from:rumination-abc123456789",
	}
	for _, s := range wantSections {
		if !strings.Contains(block.Text, s) {
			t.Errorf("block missing %q:\n%s", s, block.Text)
		}
	}

	if block.CandidateID != c.ID {
		t.Errorf("block.CandidateID = %s, want %s", block.CandidateID, c.ID)
	}
	if block.Target != target {
		t.Errorf("block.Target mismatch")
	}
	if block.TokenEstimate <= 0 {
		t.Errorf("token estimate should be positive, got %d", block.TokenEstimate)
	}
}

func TestPack_ObservationTarget(t *testing.T) {
	// Targets that are observations point the agent at mnemos_save with a
	// supersedes link, not mnemos_skill_save. Verify the action text
	// dispatches correctly.
	c := Candidate{
		ID:          "rumination-deadbeef1234",
		MonitorName: "contradiction-detected",
		Severity:    SeverityMedium,
		Reason:      "new correction contradicts convention",
		TargetKind:  TargetObservation,
		TargetID:    "obs-conv",
	}
	target := TargetRef{
		Kind: TargetObservation,
		ID:   "obs-conv",
		Name: "always wrap errors with %w",
		Body: "All errors wrapped with fmt.Errorf(..., %w, err).",
	}
	block := Pack(c, target)

	if !strings.Contains(block.Text, "mnemos_save") {
		t.Errorf("observation block should reference mnemos_save:\n%s", block.Text)
	}
	if !strings.Contains(block.Text, "supersedes=obs-conv") {
		t.Errorf("observation block should hint at supersedes link:\n%s", block.Text)
	}
	if strings.Contains(block.Text, "mnemos_skill_save") {
		t.Errorf("observation block should not reference skill save:\n%s", block.Text)
	}
}

func TestPack_EmptyEvidence(t *testing.T) {
	// Degenerate case: a candidate with no evidence should still render a
	// well-formed block (just without the evidence section). Keeps Pack
	// defensive against future monitors that encode their signal in the
	// Reason field alone.
	c := Candidate{
		ID:          "rumination-no-evidence",
		MonitorName: "stale-skill",
		Severity:    SeverityLow,
		Reason:      "no uses in 120 days",
		TargetKind:  TargetSkill,
		TargetID:    "sk1",
	}
	target := TargetRef{Kind: TargetSkill, ID: "sk1", Name: "dormant", Body: "do a thing"}
	block := Pack(c, target)

	if strings.Contains(block.Text, "## Disconfirming evidence") {
		t.Errorf("empty evidence should omit the section:\n%s", block.Text)
	}
	if !strings.Contains(block.Text, "## Hostile review") {
		t.Errorf("hostile review section is mandatory even without evidence:\n%s", block.Text)
	}
}

func TestHostilePrompts_AreAdversarial(t *testing.T) {
	// Sanity check: the prompts must push the agent toward falsification,
	// not polite inquiry. A change that makes them softer should break this.
	prompts := hostilePrompts()
	if len(prompts) < 3 {
		t.Fatalf("want at least 3 prompts, got %d", len(prompts))
	}
	wantWords := []string{"Steelman", "fatal flaw", "falsify"}
	joined := strings.Join(prompts, " ")
	for _, w := range wantWords {
		if !strings.Contains(joined, w) {
			t.Errorf("prompts missing adversarial cue %q:\n%s", w, joined)
		}
	}
}

func TestOneLine_CollapsesAndTruncates(t *testing.T) {
	in := "first line\n  second    line\nthird"
	got := oneLine(in)
	if got != "first line second line third" {
		t.Errorf("collapse mismatch: %q", got)
	}

	long := strings.Repeat("x", 300)
	out := oneLine(long)
	if runes := utf8.RuneCountInString(out); runes > 241 {
		t.Errorf("truncate failed: %d runes", runes)
	}
	if !strings.HasSuffix(out, "…") {
		t.Errorf("truncate should add ellipsis: %q", out)
	}
}
