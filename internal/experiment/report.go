package experiment

import (
	"fmt"
	"math"
)

// Arm names, matching session.ArmPush and session.ArmIndex.
const (
	ArmPush  = "push"
	ArmIndex = "index"
)

// MinConversationsPerArm is the size below which the report declines to read
// the result. Under it the interval is too wide and too rough to mean much.
const MinConversationsPerArm = 20

// minPromptsForRate keeps very short conversations out of the pushback rate,
// where one prompt would score 0 or 100 and swamp the mean.
const minPromptsForRate = 3

// Conversation is one assigned conversation and what its transcript showed.
type Conversation struct {
	HostID  string
	Arm     string
	Project string
	Metrics Metrics
	// Found is false when the transcript could not be located.
	Found bool
}

// ArmSummary totals one arm.
type ArmSummary struct {
	Arm string
	// Assigned counts conversations given this arm, Measured those with a
	// transcript and at least one typed prompt.
	Assigned int
	Measured int
	Totals   Metrics
}

// Comparison is the index-minus-push difference on one measure, with a 95%
// interval on conversation-level means.
type Comparison struct {
	Name    string
	Primary bool
	Push    float64
	Index   float64
	Diff    float64
	Low     float64
	High    float64
	// N counts the conversations behind each mean.
	PushN  int
	IndexN int
}

// Report is the full comparison.
type Report struct {
	Push        ArmSummary
	Index       ArmSummary
	Comparisons []Comparison
	Verdict     string
}

// Build compares the arms.
func Build(conversations []Conversation) Report {
	report := Report{
		Push:  ArmSummary{Arm: ArmPush},
		Index: ArmSummary{Arm: ArmIndex},
	}
	groups := map[string][]Metrics{}
	for _, c := range conversations {
		summary := report.summaryFor(c.Arm)
		if summary == nil {
			continue
		}
		summary.Assigned++
		if !c.Found || c.Metrics.Prompts == 0 {
			continue
		}
		summary.Measured++
		summary.Totals.add(c.Metrics)
		groups[c.Arm] = append(groups[c.Arm], c.Metrics)
	}
	push, index := groups[ArmPush], groups[ArmIndex]

	mentions := compare("memories mentioned in replies, per conversation", push, index, func(m Metrics) float64 { return float64(m.Mentioned) })
	mentions.Primary = true
	report.Comparisons = []Comparison{
		mentions,
		comparePushback(push, index),
		compare("mnemos_get opens, per conversation", push, index, func(m Metrics) float64 { return float64(m.Opens) }),
		compare("mnemos_search calls, per conversation", push, index, func(m Metrics) float64 { return float64(m.Searches) }),
		compare("characters injected by hooks, per conversation", push, index, func(m Metrics) float64 { return float64(m.InjectedChars) }),
	}
	report.Verdict = verdict(report)
	return report
}

func (r *Report) summaryFor(arm string) *ArmSummary {
	switch arm {
	case ArmPush:
		return &r.Push
	case ArmIndex:
		return &r.Index
	}
	return nil
}

func (m *Metrics) add(other Metrics) {
	m.Prompts += other.Prompts
	m.Pushback += other.Pushback
	m.Opens += other.Opens
	m.Searches += other.Searches
	m.NoteReads += other.NoteReads
	m.Mentioned += other.Mentioned
	m.InjectedChars += other.InjectedChars
}

func comparePushback(push, index []Metrics) Comparison {
	long := func(group []Metrics) []Metrics {
		var out []Metrics
		for _, m := range group {
			if m.Prompts >= minPromptsForRate {
				out = append(out, m)
			}
		}
		return out
	}
	return compare("prompts pushing back, per 100 prompts (guardrail)", long(push), long(index), func(m Metrics) float64 {
		return 100 * float64(m.Pushback) / float64(m.Prompts)
	})
}

func compare(name string, push, index []Metrics, value func(Metrics) float64) Comparison {
	pushMean, pushVar := meanVariance(push, value)
	indexMean, indexVar := meanVariance(index, value)
	c := Comparison{
		Name: name, Push: pushMean, Index: indexMean, Diff: indexMean - pushMean,
		PushN: len(push), IndexN: len(index),
	}
	if len(push) > 1 && len(index) > 1 {
		margin := 1.96 * math.Sqrt(pushVar/float64(len(push))+indexVar/float64(len(index)))
		c.Low, c.High = c.Diff-margin, c.Diff+margin
	}
	return c
}

// meanVariance returns the mean and sample variance of value over group.
func meanVariance(group []Metrics, value func(Metrics) float64) (mean, variance float64) {
	if len(group) == 0 {
		return 0, 0
	}
	for _, m := range group {
		mean += value(m)
	}
	mean /= float64(len(group))
	if len(group) < 2 {
		return mean, 0
	}
	for _, m := range group {
		d := value(m) - mean
		variance += d * d
	}
	return mean, variance / float64(len(group)-1)
}

// verdict reads the primary measure, then the guardrail. Memory reaching the
// work is the primary because it can move far enough to see in a few weeks.
// Pushback is too rare for that: with 30 conversations an arm only a very large
// change would clear the interval, so it can only warn.
func verdict(r Report) string {
	mentions, pushback := r.Comparisons[0], r.Comparisons[1]
	if mentions.PushN < MinConversationsPerArm || mentions.IndexN < MinConversationsPerArm {
		return fmt.Sprintf(
			"Too early to read: %d push and %d index conversations so far, and this needs %d in each.",
			mentions.PushN, mentions.IndexN, MinConversationsPerArm)
	}
	var result string
	switch {
	case mentions.Low > 0:
		result = fmt.Sprintf("Index sessions used more memory in their replies, %.2f more per conversation.", mentions.Diff)
	case mentions.High < 0:
		result = fmt.Sprintf("Push sessions used more memory in their replies, %.2f more per conversation.", -mentions.Diff)
	default:
		result = "No measurable difference in how much memory reached the replies, so choose on cost."
	}
	if pushback.PushN > 1 && pushback.IndexN > 1 && pushback.Low > 0 {
		result += fmt.Sprintf(" Warning: index sessions drew more pushback, %.1f more per 100 prompts.", pushback.Diff)
	}
	if pushback.PushN > 1 && pushback.IndexN > 1 && pushback.High < 0 {
		result += fmt.Sprintf(" Warning: push sessions drew more pushback, %.1f more per 100 prompts.", -pushback.Diff)
	}
	return result
}
