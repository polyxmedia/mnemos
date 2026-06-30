package memory

// SpecVersion identifies the mcp-memory-spec revision this build targets.
// Provisional: the spec is a working draft (docs/spec/mcp-memory-spec-draft.md)
// and the tier names below are not yet ratified.
const SpecVersion = "mcp-memory-spec/draft-0"

// Conformance tier names (provisional; see docs/spec/mcp-memory-spec-draft.md §9).
const (
	tierCore       = "memory-core"
	tierProvenance = "provenance"
	tierEmbeddings = "embeddings"
	tierFederation = "federation"
)

// Capabilities is an honest, behaviour-bound statement of which parts of the
// mcp-memory-spec this store actually satisfies right now. The Tiers list is
// DERIVED from the flags below, never hardcoded: a hybrid-capable build that
// has lost its embedder reports HybridRetrieval=false and drops the embeddings
// tier, so the document can never claim a capability the store cannot deliver
// at call time. That honesty is the whole point of advertising capabilities;
// it is precisely the property a hardcoded compliance string fails to keep.
type Capabilities struct {
	Spec           string   `json:"spec"`
	Implementation string   `json:"implementation"`
	Tiers          []string `json:"tiers"`

	// Schema-level guarantees. Unconditional for this implementation: the
	// bi-temporal columns, provenance substrate, trust tiers, typed links,
	// and content-hash dedup are fixed parts of the schema.
	Bitemporal         bool `json:"bitemporal"`
	Provenance         bool `json:"provenance"`
	TrustTiers         bool `json:"trust_tiers"`
	TypedLinks         bool `json:"typed_links"`
	DeterministicDedup bool `json:"deterministic_dedup"`

	// HybridRetrieval is runtime-bound: true only when an embedder is
	// configured and dimensioned, so it tracks what search can actually do.
	HybridRetrieval bool `json:"hybrid_retrieval"`

	// Federation (fidelity-preserving cross-store export/import) is unbuilt.
	// ROADMAP Bet 2 phase 4.
	Federation bool `json:"federation"`
}

// Capabilities returns the live capability document for this service. Read it
// fresh each call so HybridRetrieval reflects current embedder state.
func (s *Service) Capabilities() Capabilities {
	c := Capabilities{
		Spec:               SpecVersion,
		Implementation:     "mnemos",
		Bitemporal:         true,
		Provenance:         true,
		TrustTiers:         true,
		TypedLinks:         true,
		DeterministicDedup: true,
		HybridRetrieval:    s.HybridEnabled(),
		Federation:         false,
	}
	c.Tiers = c.tiers()
	return c
}

// tiers derives the conformance tier list from the capability flags. Reading
// the flags rather than a constant is what keeps the advertised tier in step
// with what the store can actually do.
func (c Capabilities) tiers() []string {
	// memory-core (save/search/get/link/invalidate + bi-temporal + dedup) is
	// always satisfied by this implementation.
	t := []string{tierCore}
	if c.Provenance && c.TrustTiers {
		t = append(t, tierProvenance)
	}
	if c.HybridRetrieval {
		t = append(t, tierEmbeddings)
	}
	if c.Federation {
		t = append(t, tierFederation)
	}
	return t
}
