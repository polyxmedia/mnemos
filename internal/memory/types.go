// Package memory holds the observation domain: the atomic unit of agent
// memory. An Observation is an agent-curated note — something the agent chose
// to remember — not a raw conversation dump.
package memory

import "time"

// ObsType classifies what kind of knowledge an observation carries. The
// taxonomy distinguishes episodic ("what happened"), semantic ("what is
// true"), and procedural ("how to do X") memory, mirroring the cognitive
// science split that agent memory research has converged on.
type ObsType string

const (
	TypeDecision     ObsType = "decision"
	TypeBugfix       ObsType = "bugfix"
	TypePattern      ObsType = "pattern"
	TypePreference   ObsType = "preference"
	TypeContext      ObsType = "context"
	TypeArchitecture ObsType = "architecture"
	TypeEpisodic     ObsType = "episodic"
	TypeSemantic     ObsType = "semantic"
	TypeProcedural   ObsType = "procedural"
	TypeCorrection   ObsType = "correction"
	TypeConvention   ObsType = "convention"
	TypeDream        ObsType = "dream"
)

// AllTypes is the canonical list of recognised observation types, in the
// order they appear in agent-facing documentation.
var AllTypes = []ObsType{
	TypeDecision, TypeBugfix, TypePattern, TypePreference, TypeContext,
	TypeArchitecture, TypeEpisodic, TypeSemantic, TypeProcedural,
	TypeCorrection, TypeConvention, TypeDream,
}

// Valid reports whether t is a recognised observation type.
func (t ObsType) Valid() bool {
	for _, known := range AllTypes {
		if t == known {
			return true
		}
	}
	return false
}

// SourceKind identifies who or what produced an observation. Bet 2
// provenance substrate — every save knows where its content came from so
// downstream code can quarantine untrusted inputs and surface chains on
// search.
type SourceKind string

const (
	// SourceUser: explicit user-authored save (typed a correction, asked
	// to remember a decision). Highest trust by default.
	SourceUser SourceKind = "user"
	// SourceTool: content derived from a tool call's output — web fetch,
	// file read, MCP server response. Untrusted by default.
	SourceTool SourceKind = "tool"
	// SourceAgentInference: the agent's own conclusion drawn from the
	// conversation. Medium trust — still the agent talking, not the user.
	SourceAgentInference SourceKind = "agent_inference"
	// SourceDream: produced by the dream / consolidation pass, typically
	// a skill or abstraction derived from multiple parent observations.
	SourceDream SourceKind = "dream"
	// SourceImport: pulled in from another store (vault sync, skill pack
	// install, migration). Trust inherited from the source system.
	SourceImport SourceKind = "import"
)

// Valid reports whether sk is a recognised source kind.
func (sk SourceKind) Valid() bool {
	switch sk {
	case SourceUser, SourceTool, SourceAgentInference, SourceDream, SourceImport:
		return true
	}
	return false
}

// TrustTier is retrieval's coarse filter on an observation. Callers that
// do not ask for raw content should only see curated and skill rows; raw
// is the quarantine zone where tool-derived and agent-inferred content
// sits until promoted.
type TrustTier string

const (
	// TrustRaw: stored, not yet validated. Excluded from prewarm and
	// default search; visible only through explicit raw-tier queries.
	TrustRaw TrustTier = "raw"
	// TrustCurated: the default. Surfaced in prewarm, search, replay.
	TrustCurated TrustTier = "curated"
	// TrustSkill: promoted into a procedural skill via the dream pass.
	// Still returned like a curated observation but flagged so consumers
	// can choose to surface skills preferentially.
	TrustSkill TrustTier = "skill"
)

// Valid reports whether t is a recognised trust tier.
func (t TrustTier) Valid() bool {
	switch t {
	case TrustRaw, TrustCurated, TrustSkill:
		return true
	}
	return false
}

// LinkType classifies an edge between two observations. 'supersedes' drives
// temporal invalidation: when A supersedes B, B's valid_until is set to A's
// valid_from.
type LinkType string

const (
	LinkRelated     LinkType = "related"
	LinkCausedBy    LinkType = "caused_by"
	LinkSupersedes  LinkType = "supersedes"
	LinkContradicts LinkType = "contradicts"
	LinkRefines     LinkType = "refines"
)

// Valid reports whether l is a recognised link type.
func (l LinkType) Valid() bool {
	switch l {
	case LinkRelated, LinkCausedBy, LinkSupersedes, LinkContradicts, LinkRefines:
		return true
	}
	return false
}

// Observation is an agent-curated memory record. All fields except Title,
// Content, and Type are optional at save time; the service fills defaults.
//
// Bi-temporal semantics:
//   - CreatedAt / InvalidatedAt: system time (when we recorded or invalidated)
//   - ValidFrom / ValidUntil:   fact time (when the observation holds true)
//
// An observation is "live" when InvalidatedAt is nil and ValidUntil is either
// nil or in the future.
type Observation struct {
	ID             string
	SessionID      string
	AgentID        string
	Project        string
	Title          string
	Content        string
	Type           ObsType
	Tags           []string
	Importance     int
	AccessCount    int
	LastAccessedAt *time.Time
	CreatedAt      time.Time
	ValidFrom      time.Time
	ValidUntil     *time.Time
	InvalidatedAt  *time.Time
	ExpiresAt      *time.Time

	// ContentHash is the SHA-256 of normalised content. Used for dedup on
	// save: identical content in the same (agent, project) scope bumps the
	// existing observation's access count instead of creating a duplicate.
	ContentHash string

	// Structured holds type-specific fields as JSON (e.g. corrections carry
	// tried/wrong_because/fix). Empty for types that only need the free-text
	// content field.
	Structured string

	// Rationale is the WHY for decisions, conventions, and architecture
	// observations. Separate from content so the rationale can be surfaced
	// specifically in pre-warm blocks without inflating context.
	Rationale string

	// Embedding is the model-generated vector for this observation. Nil when
	// embeddings are disabled. Stored as BLOB on disk.
	Embedding      []float32
	EmbeddingModel string

	// LastExportedAt tracks when this observation was written to the
	// Obsidian vault. NULL means never exported.
	LastExportedAt *time.Time

	// SourceKind, TrustTier, and DerivedFrom form the provenance
	// substrate (Bet 2 phase 1). SourceKind records who produced the
	// content; TrustTier gates retrieval; DerivedFrom chains parent
	// observation IDs so a raw→curated→skill DAG can be traversed.
	SourceKind  SourceKind
	TrustTier   TrustTier
	DerivedFrom []string
}

// Live reports whether the observation is currently valid at t.
func (o Observation) Live(t time.Time) bool {
	if o.InvalidatedAt != nil {
		return false
	}
	if o.ValidUntil != nil && !o.ValidUntil.After(t) {
		return false
	}
	if o.ExpiresAt != nil && !o.ExpiresAt.After(t) {
		return false
	}
	return true
}

// SaveInput is the narrow set of fields an agent provides when calling save.
// The service fills ID, timestamps, defaults, and session binding.
type SaveInput struct {
	SessionID  string
	AgentID    string
	Project    string
	Title      string
	Content    string
	Type       ObsType
	Tags       []string
	Importance int
	ValidFrom  *time.Time
	ValidUntil *time.Time
	TTLDays    int

	// Structured fields for type-specific data (e.g. correction.tried).
	// JSON-encoded object; keys are type-specific.
	Structured string

	// Rationale for decisions/conventions/architecture.
	Rationale string

	// Provenance fields (Bet 2 phase 1). Unspecified values default to
	// SourceUser / TrustCurated / nil in the service — back-compat with
	// every existing caller that predates the substrate.
	SourceKind  SourceKind
	TrustTier   TrustTier
	DerivedFrom []string
}

// SaveResult is the outcome of Save: the resulting observation plus whether
// this was a dedup (existing row was reused) or a fresh insert.
type SaveResult struct {
	Observation *Observation
	Deduped     bool // true when the content already existed; AccessCount bumped instead of inserting
}

// SearchInput parameterises a search query. Query is the BM25 pattern; filters
// narrow the candidate set before ranking.
type SearchInput struct {
	Query         string
	AgentID       string
	Project       string
	Type          ObsType
	Tags          []string
	MinImportance int
	Limit         int
	IncludeStale  bool      // include invalidated / expired observations
	AsOf          time.Time // historical query; zero value means "now"
}

// SearchResult is a ranked hit with a content snippet and score breakdown.
// Agents receive these from Search; Get returns the full Observation.
type SearchResult struct {
	Observation Observation
	Score       float64
	BM25        float64
	Snippet     string
}

// ContextInput parameterises mnemos_context: agent gets a token-budgeted
// block of relevant memory.
type ContextInput struct {
	Query     string
	AgentID   string
	Project   string
	MaxTokens int
}

// ContextBlock is a prepared context string ready for injection, with the
// observations it was built from for provenance.
type ContextBlock struct {
	Text          string
	TokenEstimate int
	Observations  []Observation
}

// Stats is the aggregate view returned by mnemos_stats.
type Stats struct {
	Observations     int64
	LiveObservations int64
	Sessions         int64
	Skills           int64
	StorageBytes     int64
	TopTags          []TagCount
	RecentSessions   []RecentSession
}

// TagCount is a single (tag, frequency) pair.
type TagCount struct {
	Tag   string
	Count int64
}

// RecentSession is a compact session summary for stats output.
type RecentSession struct {
	ID        string
	Project   string
	Goal      string
	StartedAt time.Time
	EndedAt   *time.Time
}

// LinkEdge is a materialised view of one row in the observation_links
// table with both endpoints' titles and agent IDs inlined. Returned from
// link-graph queries (e.g. monitor_contradiction's scan) so callers do
// not have to round-trip to observations on every edge they inspect.
type LinkEdge struct {
	LinkType        LinkType
	SourceID        string
	SourceTitle     string
	SourceAgent     string
	SourceCreatedAt time.Time
	TargetID        string
	TargetTitle     string
	TargetAgent     string
	TargetCreatedAt time.Time
	LinkedAt        time.Time
}

// FileTouch records that an agent touched a file during a session.
type FileTouch struct {
	Project   string
	AgentID   string
	Path      string
	SessionID string
	Note      string
	TouchedAt time.Time
}

// TouchInput is the write-side payload for recording a file touch.
type TouchInput struct {
	Project   string
	AgentID   string
	Path      string
	SessionID string
	Note      string
}

// HotFile aggregates touches for a (project, path) pair.
type HotFile struct {
	Project     string
	AgentID     string
	Path        string
	TouchCount  int
	LastTouched time.Time
}
