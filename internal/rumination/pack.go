package rumination

import (
	"fmt"
	"strings"
)

// Pack renders a Candidate into a review Block. It is a pure function
// over (Candidate, TargetRef) so tests can cover the rendering exhaustively
// without touching any store. The Service resolves the TargetRef and then
// calls this.
//
// The block has five sections in this order:
//
//  1. Hypothesis — the current version verbatim, so the agent reasons
//     about the actual text, not a paraphrase.
//  2. Disconfirming evidence — what the monitor gathered.
//  3. Falsifiable restatement — reframes the hypothesis as a prediction
//     that the evidence already tested. Forces the scientific-method
//     framing structurally; the agent cannot skip to synthesis without
//     first deciding whether the prediction survived.
//  4. Hostile review prompts — adversarial questions the agent is
//     required to answer before proposing a revision.
//  5. Action — the concrete tool call the revision should be written
//     through, with the `ruminated-from:<id>` provenance tag.
func Pack(c Candidate, target TargetRef) Block {
	var b strings.Builder

	fmt.Fprintf(&b, "# Rumination · %s\n\n", target.Name)
	fmt.Fprintf(&b, "_Triggered by **%s** (severity %s): %s_\n\n", c.MonitorName, c.Severity, c.Reason)

	b.WriteString("## Hypothesis under review\n\n")
	b.WriteString(strings.TrimRight(target.Body, "\n"))
	b.WriteString("\n\n")

	if len(c.Evidence) > 0 {
		b.WriteString("## Disconfirming evidence\n\n")
		for _, e := range c.Evidence {
			fmt.Fprintf(&b, "- **%s** — %s\n", e.Label, oneLine(e.Content))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Falsifiable restatement\n\n")
	b.WriteString("If the hypothesis holds as written, the evidence above should not have occurred. It did. Either the rule is wrong, or the rule is right but stated too generally to guide behaviour in the situations that fired this rumination. The revision must survive the evidence — not sidestep it.\n\n")

	b.WriteString("## Hostile review — answer before proposing a revision\n\n")
	for _, q := range hostilePrompts() {
		fmt.Fprintf(&b, "- %s\n", q)
	}
	b.WriteString("\n")

	b.WriteString("## Action\n\n")
	b.WriteString(actionText(c, target))

	text := b.String()
	return Block{
		CandidateID:   c.ID,
		Target:        target,
		Text:          text,
		TokenEstimate: (len(text) + 3) / 4,
	}
}

// hostilePrompts are the adversarial questions baked into every review
// block. They are ordered roughly steel-man → falsification → scope →
// synthesis so the agent cannot shortcut to the revision without first
// interrogating whether one is warranted at all.
func hostilePrompts() []string {
	return []string{
		"Steelman the opposite rule. What is the strongest argument that the inverse is correct, or that the rule should not exist at all?",
		"Identify the fatal flaw. Where does the current procedure break down on a realistic input the evidence points to?",
		"Is the evidence sufficient to falsify the hypothesis, or is it a noise-level anomaly? State which and justify.",
		"Has the surrounding context shifted since this rule was recorded such that its premise no longer holds?",
		"What concrete prediction would the revised rule make that the current one does not? If it makes no new prediction, the revision is cosmetic — stop.",
	}
}

// actionText is the closing section that tells the agent exactly how to
// record the revision. The provenance tag is the binding contract: a
// future rumination pass finds the resolved-by version via this tag and
// closes the candidate instead of refiring.
func actionText(c Candidate, target TargetRef) string {
	tag := "ruminated-from:" + c.ID
	switch target.Kind {
	case TargetSkill:
		return fmt.Sprintf(
			"Propose the revised procedure via `mnemos_skill_save` with the **same name** (`%s` — mnemos will version-bump) and add the tag `%s`. If the right outcome is to retire the rule, record a correction explaining why and let the skill decay out naturally.\n",
			target.Name, tag,
		)
	case TargetObservation:
		return fmt.Sprintf(
			"Propose the revised observation via `mnemos_save` with `supersedes=%s` and tag `%s`. Mnemos will set `invalidated_at` on the original automatically via the supersedes link. If the right outcome is retirement rather than revision, invalidate the target explicitly.\n",
			target.ID, tag,
		)
	default:
		return fmt.Sprintf("Propose the revision via the appropriate mutation tool with tag `%s`.\n", tag)
	}
}

// oneLine collapses whitespace and caps length for evidence bullets so a
// verbose correction body does not blow up the block.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}
