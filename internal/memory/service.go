package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Service is the agent-facing API over observations. It owns ID assignment,
// timestamp defaults, ranking, supersession, and token-budgeted context
// packing. Transports (MCP, HTTP, CLI) call Service methods, never the Store
// directly.
type Service struct {
	store  Store
	ranker *Ranker
	clock  func() time.Time
}

// Config bundles injected dependencies for the memory service.
type Config struct {
	Store       Store
	RankParams  RankParams
	Clock       func() time.Time
	AgentID     string
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
	return &Service{
		store:  cfg.Store,
		ranker: NewRanker(params),
		clock:  cfg.Clock,
	}
}

// Save creates a new observation from agent-provided input. ID is a ULID so
// results sort naturally by time, and timestamps default to now.
func (s *Service) Save(ctx context.Context, in SaveInput) (*Observation, error) {
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

	o := &Observation{
		ID:         ulid.Make().String(),
		SessionID:  in.SessionID,
		AgentID:    defaultString(in.AgentID, "default"),
		Project:    in.Project,
		Title:      in.Title,
		Content:    in.Content,
		Type:       in.Type,
		Tags:       in.Tags,
		Importance: in.Importance,
		CreatedAt:  now,
		ValidFrom:  validFrom,
		ValidUntil: in.ValidUntil,
	}
	if in.TTLDays > 0 {
		t := now.AddDate(0, 0, in.TTLDays)
		o.ExpiresAt = &t
	}

	if err := s.store.Insert(ctx, o); err != nil {
		return nil, err
	}
	return o, nil
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

// Search runs the ranking layer over raw BM25 hits and returns the top
// results sorted by composite score.
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
	for i := range raw {
		raw[i].Score = s.ranker.Score(raw[i].Observation, raw[i].BM25, now)
	}
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].Score > raw[j].Score })

	if len(raw) > limit {
		raw = raw[:limit]
	}
	return raw, nil
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
