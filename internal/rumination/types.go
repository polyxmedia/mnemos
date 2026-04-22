// Package rumination is the destructive counterpart to the dream
// consolidation pass. Where dream grows memory (corrections cluster into
// skills), rumination prunes and revises: stored knowledge whose
// effectiveness has fallen below a configurable threshold gets packaged
// with its disconfirming evidence and adversarial prompts so the agent
// can run a hostile peer review and propose a better version.
//
// The memory layer stays LLM-free. Rumination detects breaches and
// packages a structured review block; the synthesis of a revised version
// happens agent-side, and the revision is written back via the existing
// mutation tools carrying a `ruminated-from:<id>` provenance tag. That
// keeps the bi-temporal invariants (old versions invalidated, never
// deleted) and the no-model-inference invariant both intact.
package rumination

import "time"

// Severity ranks how urgent a rumination candidate is. Monitors compute it
// from how far past the breach threshold a target has drifted. Higher
// severities float to the top of the candidate list and the prewarm
// surface once one is wired up.
type Severity int

const (
	SeverityLow Severity = iota + 1
	SeverityMedium
	SeverityHigh
)

// String returns the wire label used in JSON output and agent-facing text.
func (s Severity) String() string {
	switch s {
	case SeverityHigh:
		return "high"
	case SeverityMedium:
		return "medium"
	case SeverityLow:
		return "low"
	default:
		return "unknown"
	}
}

// TargetKind names the class of entity under review. Distinguishing kinds
// lets the service dispatch to the right store when resolving a target
// body for Pack without a type switch at the call site.
type TargetKind string

const (
	TargetSkill       TargetKind = "skill"
	TargetObservation TargetKind = "observation"
)

// Candidate is one detected threshold breach. Monitors emit Candidates;
// the Service packages each into a Block on demand. Candidates are thin
// by design: they carry identity and reason but not the full target body,
// so a dream loop can queue or persist them cheaply and resolve bodies
// lazily when the agent actually asks to ruminate.
type Candidate struct {
	// ID is a stable identifier per (monitor, target) pair so repeat
	// detection runs produce the same ID and downstream code can dedupe
	// without carrying any extra state.
	ID string

	// MonitorName is which monitor detected the breach. Mirrored into the
	// packaged block header so the agent can see why this showed up.
	MonitorName string

	Severity Severity

	// Reason is the one-line summary the monitor composed when it decided
	// to fire — e.g. "effectiveness 0.25 after 12 uses (floor 0.30)". It
	// appears under the block header verbatim.
	Reason string

	TargetKind TargetKind
	TargetID   string

	// Evidence is the disconfirming data the monitor gathered while
	// deciding to fire. Pack renders this directly as a bullet list.
	Evidence []Evidence

	DetectedAt time.Time

	// Lifecycle fields — populated by the store on read. Monitors leave
	// these zero-valued; Upsert sets Status to pending on insert, Resolve/
	// Dismiss flip the relevant pair. A monitor that wants to know whether
	// the target is already under a live rumination uses PendingByTarget,
	// not these fields.
	Status          Status
	UpdatedAt       time.Time
	ResolvedBy      string
	ResolvedAt      time.Time
	DismissedReason string
	DismissedAt     time.Time
}

// Evidence is one disconfirming record: a failed use, a contradicting
// correction, a stale-use history row. Source is an identifier pointer so
// the agent can trace the evidence back to its origin observation or
// skill if it wants to dig in further.
type Evidence struct {
	Label   string
	Content string
	Source  string
}

// Block is the review package handed to the agent: hypothesis + evidence
// + falsifiable restatement + adversarial prompts. The agent reads this,
// answers the prompts, and writes a proposed revision back through the
// normal mutation tools tagged `ruminated-from:<candidate_id>`.
type Block struct {
	CandidateID   string
	Target        TargetRef
	Text          string
	TokenEstimate int
}

// TargetRef is the resolved pointer to whatever is under review: the
// canonical identity and the verbatim current body Pack needs to render
// the hypothesis section.
type TargetRef struct {
	Kind TargetKind
	ID   string
	Name string
	Body string
}
