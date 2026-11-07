package experiment

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
)

// Arm names, matching session.ArmPush and session.ArmIndex.
const (
	ArmPush  = "push"
	ArmIndex = "index"
)

// The reading rules below were written down before the data came in
// (docs/experiment.md). Changing them after looking at a result turns the
// experiment into a search for the answer someone wanted.
const (
	// MinConversationsPerArm and MinPromptsPerArm are the size below which
	// the report declines to read the result.
	MinConversationsPerArm = 40
	MinPromptsPerArm       = 400
	// maxTopTwoShare is the share of an arm's prompts its two longest
	// conversations may hold. Above it, two conversations decide the result.
	maxTopTwoShare = 0.5
	// minHookCoverage is the share of typed prompts that must have a
	// prompt hook event. Below it the event log missed deliveries, so the
	// window includes time before the log existed or a profile without hooks.
	minHookCoverage = 0.8
	// maxMissingShare is the share of assigned conversations an arm may have
	// without a transcript before the gap is worth flagging.
	maxMissingShare = 0.2
	// bootstrapResamples and bootstrapSeed fix the interval: the same data
	// always gives the same report.
	bootstrapResamples = 2000
	bootstrapSeed      = 20261107
)

// Delivery is what the event log recorded mnemos putting into one
// conversation's context.
type Delivery struct {
	// PromptEvents counts prompts that reached the prompt hook, on either
	// arm, and PromptFires those where it showed memory.
	PromptEvents int
	PromptFires  int
	// CueFires counts cue firings, PreToolFires pre-edit search firings.
	CueFires     int
	PreToolFires int
	// Chars totals what every channel injected, the session start included.
	Chars int
}

func (d *Delivery) add(o Delivery) {
	d.PromptEvents += o.PromptEvents
	d.PromptFires += o.PromptFires
	d.CueFires += o.CueFires
	d.PreToolFires += o.PreToolFires
	d.Chars += o.Chars
}

// Conversation is one assigned conversation and what its transcript and the
// event log showed.
type Conversation struct {
	HostID  string
	Arm     string
	Project string
	Metrics Metrics
	// Found is false when the transcript could not be located.
	Found bool
	// Excluded says why the conversation is left out, empty when it counts.
	Excluded string
	Delivery Delivery
}

// ArmSummary totals one arm.
type ArmSummary struct {
	Arm string
	// Assigned counts conversations given this arm. Excluded were left out on
	// purpose, Missing had no transcript, and Measured had a transcript and
	// at least one typed prompt.
	Assigned int
	Excluded int
	Missing  int
	Measured int
	Totals   Metrics
	Delivery Delivery
	// TopTwoShare is the share of prompts from the two longest conversations.
	TopTwoShare float64
	// HookCoverage is prompt hook events per typed prompt.
	HookCoverage float64
}

// Role says how a comparison is used in the decision.
type Role string

// Comparison roles.
const (
	RolePrimary   Role = "primary"
	RoleGuardrail Role = "guardrail"
	RoleCost      Role = "cost"
	RoleContext   Role = "context"
)

// Comparison is the index-minus-push difference in one rate, pooled over
// prompts, with a 95% interval from resampling whole conversations.
type Comparison struct {
	Name  string
	Role  Role
	Push  float64
	Index float64
	Diff  float64
	Low   float64
	High  float64
	// HasInterval is false when an arm has fewer than two conversations.
	HasInterval bool
	// PushN and IndexN count the conversations behind each rate.
	PushN  int
	IndexN int
}

// Report is the full comparison.
type Report struct {
	Push        ArmSummary
	Index       ArmSummary
	Comparisons []Comparison
	// Problems are data quality failures. Any of them stops a reading.
	Problems []string
	// Warnings are worth knowing and do not stop a reading.
	Warnings []string
	Ready    bool
	Verdict  string
	// Decision is what the pre-registered rule says to do.
	Decision string
}

// measure is one rate per typed prompt.
type measure struct {
	name  string
	role  Role
	scale float64
	num   func(Conversation) float64
}

var measures = []measure{
	{"memories mentioned in replies, per 100 prompts", RolePrimary, 100, func(c Conversation) float64 { return float64(c.Metrics.Mentioned) }},
	{"prompts pushing back, per 100 prompts", RoleGuardrail, 100, func(c Conversation) float64 { return float64(c.Metrics.Pushback) }},
	{"characters injected by mnemos, per prompt", RoleCost, 1, func(c Conversation) float64 { return float64(c.Delivery.Chars) }},
	{"mnemos_get opens, per 100 prompts", RoleContext, 100, func(c Conversation) float64 { return float64(c.Metrics.Opens) }},
	{"mnemos_search calls, per 100 prompts", RoleContext, 100, func(c Conversation) float64 { return float64(c.Metrics.Searches) }},
}

// Build compares the arms.
func Build(conversations []Conversation) Report {
	report := Report{
		Push:  ArmSummary{Arm: ArmPush},
		Index: ArmSummary{Arm: ArmIndex},
	}
	groups := map[string][]Conversation{}
	for _, c := range conversations {
		summary := report.summaryFor(c.Arm)
		if summary == nil {
			continue
		}
		summary.Assigned++
		switch {
		case c.Excluded != "":
			summary.Excluded++
			continue
		case !c.Found:
			summary.Missing++
			continue
		case c.Metrics.Prompts == 0:
			continue
		}
		summary.Measured++
		summary.Totals.add(c.Metrics)
		summary.Delivery.add(c.Delivery)
		groups[c.Arm] = append(groups[c.Arm], c)
	}
	push, index := groups[ArmPush], groups[ArmIndex]
	report.Push.finish(push)
	report.Index.finish(index)

	for i, m := range measures {
		report.Comparisons = append(report.Comparisons, compare(m, push, index, uint64(i)))
	}
	report.checkQuality()
	report.decide()
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

func (s *ArmSummary) finish(group []Conversation) {
	if s.Totals.Prompts == 0 {
		return
	}
	prompts := make([]int, 0, len(group))
	for _, c := range group {
		prompts = append(prompts, c.Metrics.Prompts)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(prompts)))
	top := 0
	for i := 0; i < len(prompts) && i < 2; i++ {
		top += prompts[i]
	}
	s.TopTwoShare = float64(top) / float64(s.Totals.Prompts)
	s.HookCoverage = float64(s.Delivery.PromptEvents) / float64(s.Totals.Prompts)
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

func compare(m measure, push, index []Conversation, stream uint64) Comparison {
	p, _ := ratio(push, m.num, nil)
	i, _ := ratio(index, m.num, nil)
	c := Comparison{
		Name: m.name, Role: m.role,
		Push: m.scale * p, Index: m.scale * i, Diff: m.scale * (i - p),
		PushN: len(push), IndexN: len(index),
	}
	if len(push) < 2 || len(index) < 2 {
		return c
	}
	rng := rand.New(rand.NewPCG(bootstrapSeed, stream))
	diffs := make([]float64, 0, bootstrapResamples)
	for b := 0; b < bootstrapResamples; b++ {
		rp, okp := ratio(push, m.num, rng)
		ri, oki := ratio(index, m.num, rng)
		if okp && oki {
			diffs = append(diffs, m.scale*(ri-rp))
		}
	}
	if len(diffs) < bootstrapResamples/2 {
		return c
	}
	sort.Float64s(diffs)
	c.Low, c.High, c.HasInterval = quantile(diffs, 0.025), quantile(diffs, 0.975), true
	return c
}

// ratio pools num over typed prompts. With rng set it does so over a
// resample of whole conversations, drawn with replacement, which keeps the
// prompts of one conversation together: they share a task, a mood and a
// project, so they are not independent observations.
func ratio(group []Conversation, num func(Conversation) float64, rng *rand.Rand) (float64, bool) {
	var n, d float64
	for k := range group {
		c := group[k]
		if rng != nil {
			c = group[rng.IntN(len(group))]
		}
		n += num(c)
		d += float64(c.Metrics.Prompts)
	}
	if d == 0 {
		return 0, false
	}
	return n / d, true
}

// quantile interpolates linearly between the order statistics of sorted.
func quantile(sorted []float64, q float64) float64 {
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return sorted[lo] + (pos-float64(lo))*(sorted[hi]-sorted[lo])
}

func (r *Report) checkQuality() {
	for _, s := range []ArmSummary{r.Push, r.Index} {
		if s.Measured == 0 {
			continue
		}
		if s.Measured > 2 && s.TopTwoShare > maxTopTwoShare {
			r.Problems = append(r.Problems, fmt.Sprintf(
				"%s arm: its two longest conversations hold %.0f%% of its prompts, so they would decide the result.",
				s.Arm, 100*s.TopTwoShare))
		}
		if s.HookCoverage < minHookCoverage {
			r.Problems = append(r.Problems, fmt.Sprintf(
				"%s arm: only %.0f%% of typed prompts have a prompt hook event. The window starts before the event log, or a profile ran without the hooks. Move experiment_start past the gap.",
				s.Arm, 100*s.HookCoverage))
		}
		if counted := s.Assigned - s.Excluded; counted > 0 && float64(s.Missing)/float64(counted) > maxMissingShare {
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"%s arm: %d of %d conversations have no transcript to read, likely from a config directory this run cannot see.",
				s.Arm, s.Missing, counted))
		}
	}
	// Assignment hashes the host session id, so each arm should get about
	// half. Three standard deviations off means assignment is broken.
	p, i := r.Push.Assigned-r.Push.Excluded, r.Index.Assigned-r.Index.Excluded
	if n := p + i; n >= 20 && math.Abs(float64(p-i)) > 3*math.Sqrt(float64(n)) {
		r.Problems = append(r.Problems, fmt.Sprintf(
			"arms are lopsided, %d push to %d index. Assignment should split about evenly.", p, i))
	}
}

func (r *Report) comparison(role Role) Comparison {
	for _, c := range r.Comparisons {
		if c.Role == role {
			return c
		}
	}
	return Comparison{}
}

// decide applies the pre-registered rule.
//
//  1. Not ready while either arm is under MinConversationsPerArm measured
//     conversations or MinPromptsPerArm prompts, or any data problem stands.
//  2. The primary measure is memories mentioned per 100 prompts. An interval
//     clear of zero names the arm whose replies used more memory.
//  3. The guardrail is pushback per 100 prompts. An interval clear of zero
//     against the arm the primary favours blocks switching to it.
//  4. With no primary difference the cheaper arm wins on characters per
//     prompt, if that interval is clear of zero and its guardrail is clean.
//     Otherwise push stays, being the default with the fewest moving parts.
func (r *Report) decide() {
	var short []string
	for _, s := range []ArmSummary{r.Push, r.Index} {
		if s.Measured < MinConversationsPerArm || s.Totals.Prompts < MinPromptsPerArm {
			short = append(short, fmt.Sprintf("%s has %d conversations and %d prompts", s.Arm, s.Measured, s.Totals.Prompts))
		}
	}
	if len(short) > 0 || len(r.Problems) > 0 {
		var why []string
		if len(short) > 0 {
			why = append(why, fmt.Sprintf("%s, and each arm needs %d and %d", strings.Join(short, ", "), MinConversationsPerArm, MinPromptsPerArm))
		}
		if len(r.Problems) > 0 {
			why = append(why, fmt.Sprintf("%d data problem(s) listed above", len(r.Problems)))
		}
		r.Verdict = "Too early to read: " + strings.Join(why, "; ") + "."
		r.Decision = "Keep the experiment running."
		return
	}
	r.Ready = true

	primary, guard, cost := r.comparison(RolePrimary), r.comparison(RoleGuardrail), r.comparison(RoleCost)
	indexWorseGuard := guard.HasInterval && guard.Low > 0
	pushWorseGuard := guard.HasInterval && guard.High < 0
	keepPush := `Keep push: set [retrieval] mode = "push".`
	useIndex := `Switch to index: set [retrieval] mode = "index".`

	switch {
	case primary.HasInterval && primary.Low > 0:
		r.Verdict = fmt.Sprintf("Index replies used more memory, %.2f more per 100 prompts.", primary.Diff)
		if indexWorseGuard {
			r.Verdict += fmt.Sprintf(" But index drew more pushback, %.1f more per 100 prompts.", guard.Diff)
			r.Decision = keepPush + " The guardrail blocks the switch."
			return
		}
		r.Decision = useIndex
	case primary.HasInterval && primary.High < 0:
		r.Verdict = fmt.Sprintf("Push replies used more memory, %.2f more per 100 prompts.", -primary.Diff)
		if pushWorseGuard {
			r.Verdict += fmt.Sprintf(" Push also drew more pushback, %.1f more per 100 prompts, so look at what it injects.", -guard.Diff)
		}
		r.Decision = keepPush
	default:
		r.Verdict = "No measurable difference in how much memory reached the replies, so the choice is on cost."
		switch {
		case cost.HasInterval && cost.High < 0 && !indexWorseGuard:
			r.Verdict += fmt.Sprintf(" Index injects %.0f fewer characters per prompt.", -cost.Diff)
			r.Decision = useIndex
		case cost.HasInterval && cost.Low > 0 && !pushWorseGuard:
			r.Verdict += fmt.Sprintf(" Push injects %.0f fewer characters per prompt.", cost.Diff)
			r.Decision = keepPush
		default:
			r.Verdict += " Neither is clearly cheaper."
			r.Decision = keepPush + " It is the default, with the fewest moving parts."
		}
	}
}
