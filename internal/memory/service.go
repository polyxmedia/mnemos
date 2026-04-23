package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Embedder is the minimal interface memory.Service needs from an embedding
// provider — the full interface lives in internal/embedding. Redeclared
// here to avoid an import cycle and keep the service boundary clean.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Dimension() int
	Model() string
}

// Service is the agent-facing API over observations. It owns ID assignment,
// timestamp defaults, ranking, supersession, and token-budgeted context
// packing. Transports (MCP, HTTP, CLI) call Service methods, never the Store
// directly.
type Service struct {
	store    Store
	ranker   *Ranker
	hybrid   HybridParams
	embedder Embedder
	clock    func() time.Time
}

// Config bundles injected dependencies for the memory service.
type Config struct {
	Store      Store
	RankParams RankParams
	Hybrid     HybridParams
	Embedder   Embedder
	Clock      func() time.Time
	AgentID    string
}

// NewService builds a Service from a Store and ranking params.
func NewService(cfg Config) *Service {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	params := cfg.RankParams
	if params == (RankParams{}) {
		params = DefaultRankParams()
	}
	hybrid := cfg.Hybrid
	if hybrid == (HybridParams{}) {
		hybrid = DefaultHybridParams()
	}
	return &Service{
		store:    cfg.Store,
		ranker:   NewRanker(params),
		hybrid:   hybrid,
		embedder: cfg.Embedder,
		clock:    cfg.Clock,
	}
}

// HybridEnabled reports whether vector search is active.
func (s *Service) HybridEnabled() bool {
	return s.embedder != nil && s.embedder.Dimension() > 0
}

// Save creates a new observation from agent-provided input, or bumps the
// access counter on an existing identical one (dedup-on-save). Returns
// SaveResult so callers can distinguish a fresh insert from a dedup hit.
func (s *Service) Save(ctx context.Context, in SaveInput) (*SaveResult, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, fmt.Errorf("save: title is required")
	}
	if strings.TrimSpace(in.Content) == "" {
		return nil, fmt.Errorf("save: content is required")
	}
	if !in.Type.Valid() {
		return nil, fmt.Errorf("save: invalid obs_type %q", in.Type)
	}
	if in.Importance == 0 {
		in.Importance = 5
	}
	if in.Importance < 1 || in.Importance > 10 {
		return nil, fmt.Errorf("save: importance must be 1..10")
	}

	now := s.clock().UTC()
	validFrom := now
	if in.ValidFrom != nil {
		validFrom = in.ValidFrom.UTC()
	}

	agent := defaultString(in.AgentID, "default")
	hash := hashContent(in.Type, in.Title, in.Content, in.Rationale, in.Structured)

	sourceKind := in.SourceKind
	if sourceKind == "" {
		sourceKind = SourceUser
	}
	if !sourceKind.Valid() {
		return nil, fmt.Errorf("save: invalid source_kind %q", sourceKind)
	}
	trustTier := in.TrustTier
	if trustTier == "" {
		trustTier = TrustCurated
	}
	if !trustTier.Valid() {
		return nil, fmt.Errorf("save: invalid trust_tier %q", trustTier)
	}

	// Dedup: if the same (agent, project, content_hash) already lives, bump
	// access and return without writing. Invalidated rows don't count — a
	// re-save can legitimately resurrect a superseded fact.
	if existing, err := s.store.FindByContentHash(ctx, agent, in.Project, hash); err != nil {
		return nil, fmt.Errorf("dedup lookup: %w", err)
	} else if existing != nil {
		if err := s.store.BumpAccess(ctx, existing.ID); err != nil {
			return nil, err
		}
		return &SaveResult{Observation: existing, Deduped: true}, nil
	}

	o := &Observation{
		ID:          ulid.Make().String(),
		SessionID:   in.SessionID,
		AgentID:     agent,
		Project:     in.Project,
		Title:       in.Title,
		Content:     in.Content,
		Type:        in.Type,
		Tags:        in.Tags,
		Importance:  in.Importance,
		CreatedAt:   now,
		ValidFrom:   validFrom,
		ValidUntil:  in.ValidUntil,
		ContentHash: hash,
		Structured:  in.Structured,
		Rationale:   in.Rationale,
		SourceKind:  sourceKind,
		TrustTier:   trustTier,
		DerivedFrom: in.DerivedFrom,
	}
	if in.TTLDays > 0 {
		t := now.AddDate(0, 0, in.TTLDays)
		o.ExpiresAt = &t
	}

	if err := s.store.Insert(ctx, o); err != nil {
		return nil, err
	}

	// Embed in the background-ish way: if an embedder is configured, try
	// to generate the vector and attach it. A failure here is non-fatal —
	// the observation still exists; hybrid search just misses this one
	// candidate until the next backfill pass.
	if s.HybridEnabled() {
		if vec, err := s.embedder.Embed(ctx, embedText(o)); err == nil && len(vec) > 0 {
			o.Embedding = vec
			o.EmbeddingModel = s.embedder.Model()
			_ = s.store.UpdateEmbedding(ctx, o.ID, s.embedder.Model(), vec)
		}
	}

	return &SaveResult{Observation: o, Deduped: false}, nil
}

// embedText assembles the text we embed for an observation. Title + content
// + rationale produces a richer signal than content alone.
func embedText(o *Observation) string {
	parts := []string{o.Title, o.Content}
	if o.Rationale != "" {
		parts = append(parts, o.Rationale)
	}
	return strings.Join(parts, "\n")
}

// hashContent produces a stable SHA-256 hex digest over the identity-
// defining fields of an observation. Normalised (whitespace trimmed) so
// trivial formatting differences still dedup.
func hashContent(t ObsType, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(t))
	h.Write([]byte{0})
	for _, p := range parts {
		h.Write([]byte(strings.TrimSpace(p)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns the full observation (also bumps access count via the store).
func (s *Service) Get(ctx context.Context, id string) (*Observation, error) {
	return s.store.Get(ctx, id)
}

// Delete removes an observation outright. Prefer Supersede for anything that
// was ever true; delete is for saves that were mistakes.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// Supersede records that newID replaces oldID: links them and invalidates
// the old observation as of now. This is the right call for "we used to do
// X, now we do Y" — preserves provenance, hides the stale fact from default
// searches.
func (s *Service) Supersede(ctx context.Context, newID, oldID string) error {
	now := s.clock().UTC()
	if err := s.store.Link(ctx, newID, oldID, LinkSupersedes); err != nil {
		return err
	}
	return s.store.Invalidate(ctx, oldID, now)
}

// Invalidate marks an observation as no longer true as of now.
func (s *Service) Invalidate(ctx context.Context, id string) error {
	return s.store.Invalidate(ctx, id, s.clock().UTC())
}

// Search runs BM25 retrieval, optionally fuses with vector similarity via
// Reciprocal Rank Fusion, and applies the recency/importance/access ranker
// on top. Hybrid mode activates automatically when an embedder is
// configured and observations have stored vectors.
func (s *Service) Search(ctx context.Context, in SearchInput) ([]SearchResult, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	// Pull a wider net than the caller asked for so ranking has room to
	// re-order. This is the cheap part; network/context cost is downstream.
	in.Limit = limit * 3

	raw, err := s.store.Search(ctx, in)
	if err != nil {
		return nil, err
	}

	now := s.clock().UTC()
	if s.HybridEnabled() && in.Query != "" {
		raw = s.fuseWithVectors(ctx, in.Query, raw)
	}
	for i := range raw {
		raw[i].Score = s.ranker.Score(raw[i].Observation, raw[i].Score, now)
	}
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].Score > raw[j].Score })

	if len(raw) > limit {
		raw = raw[:limit]
	}
	return raw, nil
}

// fuseWithVectors embeds the query and re-ranks BM25 candidates via RRF,
// mixing the two ranks according to HybridParams. Candidates missing
// embeddings get their cosine rank set to infinity (no semantic signal,
// BM25 alone carries them).
func (s *Service) fuseWithVectors(ctx context.Context, query string, cands []SearchResult) []SearchResult {
	qvec, err := s.embedder.Embed(ctx, query)
	if err != nil || len(qvec) == 0 {
		// Fail open: BM25-only re-rank keeps search working.
		return cands
	}

	// Score each candidate by cosine against the query.
	type ranked struct {
		idx      int
		cosine   float64
		bm25Rank int
		cosRank  int
	}
	items := make([]ranked, len(cands))
	for i := range cands {
		items[i] = ranked{
			idx:      i,
			cosine:   cosine(cands[i].Observation.Embedding, qvec),
			bm25Rank: i + 1, // BM25 list is already sorted best-first
		}
	}
	// Sort a copy by cosine desc to compute cos ranks; observations without
	// an embedding land at the bottom with cosRank = 0 (treated as "miss").
	cosSorted := append([]ranked(nil), items...)
	sort.SliceStable(cosSorted, func(i, j int) bool { return cosSorted[i].cosine > cosSorted[j].cosine })
	for rank, item := range cosSorted {
		if len(cands[item.idx].Observation.Embedding) == 0 {
			continue
		}
		items[item.idx].cosRank = rank + 1
	}

	// Write RRF score into BM25 field so the downstream Ranker multiplier
	// treats the fused rank-signal as the "relevance base".
	for _, it := range items {
		cands[it.idx].Score = rrfScore(it.bm25Rank, it.cosRank, s.hybrid)
	}
	return cands
}

// Context returns a pre-budgeted block of memory ready for injection into
// agent context. The block never exceeds MaxTokens (estimated at ~4 chars
// per token), and items are included in descending rank until the budget is
// spent.
func (s *Service) Context(ctx context.Context, in ContextInput) (*ContextBlock, error) {
	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2000
	}

	results, err := s.Search(ctx, SearchInput{
		Query:   in.Query,
		AgentID: in.AgentID,
		Project: in.Project,
		Limit:   50,
	})
	if err != nil {
		return nil, err
	}

	block := &ContextBlock{}
	var sb strings.Builder
	budget := maxTokens
	for _, r := range results {
		entry := formatContextEntry(r.Observation)
		cost := estimateTokens(entry)
		if cost > budget {
			continue
		}
		sb.WriteString(entry)
		sb.WriteString("\n\n")
		budget -= cost
		block.Observations = append(block.Observations, r.Observation)
		if budget < 64 {
			break
		}
	}
	block.Text = strings.TrimRight(sb.String(), "\n")
	block.TokenEstimate = maxTokens - budget
	return block, nil
}

// Stats proxies to the store and tags live/total counts.
func (s *Service) Stats(ctx context.Context) (Stats, error) {
	return s.store.Stats(ctx)
}

// Prune removes expired observations.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	return s.store.Prune(ctx, s.clock().UTC())
}

// Link records an arbitrary edge between two observations.
func (s *Service) Link(ctx context.Context, sourceID, targetID string, linkType LinkType) error {
	if !linkType.Valid() {
		return fmt.Errorf("invalid link type %q", linkType)
	}
	if linkType == LinkSupersedes {
		return s.Supersede(ctx, sourceID, targetID)
	}
	return s.store.Link(ctx, sourceID, targetID, linkType)
}

func formatContextEntry(o Observation) string {
	tags := ""
	if len(o.Tags) > 0 {
		tags = " [" + strings.Join(o.Tags, ",") + "]"
	}
	return fmt.Sprintf("## %s (%s)%s\n%s", o.Title, o.Type, tags, o.Content)
}

// estimateTokens is a conservative ~4 chars/token heuristic. Not exact, but
// good enough for budget-sized decisions; the MCP layer can swap in a real
// tokenizer later without touching callers.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

func defaultString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
