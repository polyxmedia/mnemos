package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// registerTools wires every Mnemos tool into the SDK server. Input
// schemas are inferred from struct tags; the SDK validates arguments
// before calling our handler.
func (s *Server) registerTools() {
	// Save / search / get / delete / link --------------------------------
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name: "mnemos_save",
		Description: "Persist an agent-curated observation (decision, bugfix, pattern, preference, " +
			"context, architecture, episodic, semantic, procedural, correction, convention). " +
			"Deliberately curated: save only things worth remembering later.",
	}, s.handleSave)

	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name: "mnemos_search",
		Description: "Hybrid BM25 + vector ranked search with recency/importance/access multipliers. " +
			"Default scope is valid-now (invalidated/expired hidden). Returns compact hits with snippet.",
	}, s.handleSearch)

	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name:        "mnemos_get",
		Description: "Fetch a full observation by ID. Bumps the access counter.",
	}, s.handleGet)

	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name: "mnemos_delete",
		Description: "Hard-delete a mistaken save. For facts that changed, use mnemos_link with " +
			"link_type=supersedes — preserves provenance and hides the stale fact from default search.",
	}, s.handleDelete)

	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name: "mnemos_link",
		Description: "Link two observations (related, caused_by, supersedes, contradicts, refines). " +
			"'supersedes' auto-invalidates the target.",
	}, s.handleLink)

	// Sessions -----------------------------------------------------------
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name: "mnemos_session_start",
		Description: "Open a session. Returns session_id AND a pre-warmed context block composed " +
			"of conventions, recent sessions, matching skills, corrections, and hot files. Push, not pull.",
	}, s.handleSessionStart)

	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name: "mnemos_session_end",
		Description: "Close a session with summary, status (ok|failed|blocked|abandoned), and " +
			"reflection. Failed sessions' observations get a ranking boost — agents learn from mistakes.",
	}, s.handleSessionEnd)

	// Context ------------------------------------------------------------
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name: "mnemos_context",
		Description: "Token-budgeted context. Default mode: query-based search-and-pack. Mode='recovery' " +
			"restores current session state after a compaction — the 'oh shit' button.",
	}, s.handleContext)

	// Agent supercharge --------------------------------------------------
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name: "mnemos_correct",
		Description: "Record a correction: tried + wrong_because + fix + trigger_context. Auto-surfaced " +
			"in session pre-warm when the session goal matches. The anti-repeat-mistakes loop.",
	}, s.handleCorrect)

	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name: "mnemos_convention",
		Description: "Declare a project convention. Auto-injected at every session_start for the " +
			"matching project. Declared once, applied forever.",
	}, s.handleConvention)

	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name: "mnemos_touch",
		Description: "Record a file touch for the heat map. Frequently-touched files get priority in " +
			"session pre-warming so the agent focuses on what actually matters.",
	}, s.handleTouch)

	// Skills -------------------------------------------------------------
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name:        "mnemos_skill_match",
		Description: "Find skills matching a task. Ranking factors in effectiveness (skills that worked before rise up).",
	}, s.handleSkillMatch)

	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name: "mnemos_skill_save",
		Description: "Save or version a reusable procedure. Keyed by (agent_id, name); same name bumps " +
			"the version.",
	}, s.handleSkillSave)

	// Rumination ---------------------------------------------------------
	// Guarded: only expose the ruminate_* surface when a rumination
	// service is wired. Makes rumination.enabled = false (in config) a
	// clean no-op instead of surfacing tools that immediately error.
	if s.cfg.Rumination != nil {
		mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
			Name: "mnemos_ruminate_list",
			Description: "List pending rumination candidates (skills whose effectiveness has " +
				"fallen below the threshold, or other threshold breaches). Ordered severity-desc, " +
				"detected-at-desc. Each candidate is a hypothesis waiting for the hostile review " +
				"ritual — fetch the full review block with mnemos_ruminate_pack.",
		}, s.handleRuminateList)

		mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
			Name: "mnemos_ruminate_pack",
			Description: "Fetch the review block for one rumination candidate: hypothesis verbatim, " +
				"disconfirming evidence, falsifiable restatement, and hostile-review prompts. Answer " +
				"the prompts before writing a revision — the structure enforces scientific-method " +
				"reasoning over the stored belief.",
		}, s.handleRuminatePack)

		mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
			Name: "mnemos_ruminate_resolve",
			Description: "Close a rumination candidate by naming the revision that replaces the " +
				"flagged belief. Requires resolved_by (the ID of the new skill version or superseding " +
				"observation) AND why_better (one sentence stating a new prediction the revision " +
				"makes that the old version did not — Popper's falsifiability guard).",
		}, s.handleRuminateResolve)

		mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
			Name: "mnemos_ruminate_dismiss",
			Description: "Close a rumination candidate as noise — the rule stands. Reason is " +
				"required; it prevents the next dream pass from re-raising the same flag without " +
				"context. Use when you've decided the disconfirming evidence was a one-off or the " +
				"rule's premise is still sound.",
		}, s.handleRuminateDismiss)
	}

	// Stats --------------------------------------------------------------
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{
		Name:        "mnemos_stats",
		Description: "System statistics: counts, top tags, recent sessions, embedding status, storage size.",
	}, s.handleStats)
}

// ---- mnemos_save -------------------------------------------------------

type saveArgs struct {
	Title      string   `json:"title" jsonschema:"short scannable label"`
	Content    string   `json:"content" jsonschema:"the memory — structure as what/why/where/learned"`
	Type       string   `json:"type" jsonschema:"decision|bugfix|pattern|preference|context|architecture|episodic|semantic|procedural|correction|convention"`
	Tags       []string `json:"tags,omitempty"`
	Importance int      `json:"importance,omitempty" jsonschema:"1..10, defaults to 5"`
	TTLDays    int      `json:"ttl_days,omitempty"`
	AgentID    string   `json:"agent_id,omitempty"`
	Project    string   `json:"project,omitempty"`
	SessionID  string   `json:"session_id,omitempty"`
	ValidFrom  string   `json:"valid_from,omitempty" jsonschema:"RFC3339 fact-time lower bound"`
	ValidUntil string   `json:"valid_until,omitempty" jsonschema:"RFC3339 fact-time upper bound"`
	Rationale  string   `json:"rationale,omitempty" jsonschema:"the WHY — surfaced in prewarm"`
}

func (s *Server) handleSave(ctx context.Context, _ *mcpsdk.CallToolRequest, a saveArgs) (*mcpsdk.CallToolResult, any, error) {
	in := memory.SaveInput{
		Title: a.Title, Content: a.Content, Type: memory.ObsType(a.Type),
		Tags: a.Tags, Importance: a.Importance, TTLDays: a.TTLDays,
		AgentID: a.AgentID, Project: a.Project, SessionID: a.SessionID,
		Rationale: a.Rationale,
	}
	if t, err := parseTime(a.ValidFrom); err != nil {
		return nil, nil, err
	} else if t != nil {
		in.ValidFrom = t
	}
	if t, err := parseTime(a.ValidUntil); err != nil {
		return nil, nil, err
	} else if t != nil {
		in.ValidUntil = t
	}
	res, err := s.cfg.Memory.Save(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"id":         res.Observation.ID,
		"title":      res.Observation.Title,
		"type":       string(res.Observation.Type),
		"created_at": res.Observation.CreatedAt,
		"deduped":    res.Deduped,
	})
}

// ---- mnemos_search -----------------------------------------------------

type searchArgs struct {
	Query         string   `json:"query"`
	Type          string   `json:"type,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	MinImportance int      `json:"min_importance,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	AgentID       string   `json:"agent_id,omitempty"`
	Project       string   `json:"project,omitempty"`
	IncludeStale  bool     `json:"include_stale,omitempty"`
	AsOf          string   `json:"as_of,omitempty"`
}

func (s *Server) handleSearch(ctx context.Context, _ *mcpsdk.CallToolRequest, a searchArgs) (*mcpsdk.CallToolResult, any, error) {
	in := memory.SearchInput{
		Query: a.Query, Type: memory.ObsType(a.Type), Tags: a.Tags,
		MinImportance: a.MinImportance, Limit: a.Limit,
		AgentID: a.AgentID, Project: a.Project, IncludeStale: a.IncludeStale,
	}
	if t, err := parseTime(a.AsOf); err != nil {
		return nil, nil, err
	} else if t != nil {
		in.AsOf = *t
	}
	results, err := s.cfg.Memory.Search(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	hits := make([]map[string]any, 0, len(results))
	for _, r := range results {
		hits = append(hits, map[string]any{
			"id":         r.Observation.ID,
			"title":      r.Observation.Title,
			"type":       string(r.Observation.Type),
			"tags":       r.Observation.Tags,
			"importance": r.Observation.Importance,
			"score":      r.Score,
			"snippet":    r.Snippet,
			"created_at": r.Observation.CreatedAt,
		})
	}
	return jsonResult(map[string]any{"results": hits})
}

// ---- mnemos_get / delete / link ---------------------------------------

type idArgs struct {
	ID string `json:"id"`
}

func (s *Server) handleGet(ctx context.Context, _ *mcpsdk.CallToolRequest, a idArgs) (*mcpsdk.CallToolResult, any, error) {
	o, err := s.cfg.Memory.Get(ctx, a.ID)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return nil, nil, fmt.Errorf("observation not found: %s", a.ID)
		}
		return nil, nil, err
	}
	return jsonResult(o)
}

func (s *Server) handleDelete(ctx context.Context, _ *mcpsdk.CallToolRequest, a idArgs) (*mcpsdk.CallToolResult, any, error) {
	if err := s.cfg.Memory.Delete(ctx, a.ID); err != nil {
		return nil, nil, err
	}
	return textResult("deleted " + a.ID)
}

type linkArgs struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	LinkType string `json:"link_type" jsonschema:"related|caused_by|supersedes|contradicts|refines"`
}

func (s *Server) handleLink(ctx context.Context, _ *mcpsdk.CallToolRequest, a linkArgs) (*mcpsdk.CallToolResult, any, error) {
	if err := s.cfg.Memory.Link(ctx, a.SourceID, a.TargetID, memory.LinkType(a.LinkType)); err != nil {
		return nil, nil, err
	}
	return textResult(fmt.Sprintf("linked %s -[%s]-> %s", a.SourceID, a.LinkType, a.TargetID))
}

// ---- sessions ----------------------------------------------------------

type sessionStartArgs struct {
	AgentID string `json:"agent_id,omitempty"`
	Project string `json:"project,omitempty" jsonschema:"enables convention auto-injection"`
	Goal    string `json:"goal,omitempty" jsonschema:"improves skill and correction matching"`
}

func (s *Server) handleSessionStart(ctx context.Context, _ *mcpsdk.CallToolRequest, a sessionStartArgs) (*mcpsdk.CallToolResult, any, error) {
	sess, err := s.cfg.Sessions.Open(ctx, session.OpenInput{
		AgentID: a.AgentID, Project: a.Project, Goal: a.Goal,
	})
	if err != nil {
		return nil, nil, err
	}
	out := map[string]any{"session_id": sess.ID, "started_at": sess.StartedAt}
	if s.cfg.Prewarm != nil {
		if block, err := s.cfg.Prewarm.Build(ctx, prewarm.Request{
			Mode:      prewarm.ModeSessionStart,
			AgentID:   a.AgentID,
			Project:   a.Project,
			Goal:      a.Goal,
			SessionID: sess.ID,
		}); err == nil && block != nil && block.Text != "" {
			out["prewarm"] = map[string]any{
				"text":           block.Text,
				"token_estimate": block.TokenEstimate,
				"section_count":  len(block.Sections),
				"safety_risk":    block.SafetyReport.MaxRisk.String(),
			}
		}
	}
	return jsonResult(out)
}

type sessionEndArgs struct {
	SessionID   string   `json:"session_id"`
	Summary     string   `json:"summary"`
	Reflection  string   `json:"reflection,omitempty" jsonschema:"transferable lessons"`
	Status      string   `json:"status,omitempty" jsonschema:"ok|failed|blocked|abandoned"`
	OutcomeTags []string `json:"outcome_tags,omitempty"`
}

func (s *Server) handleSessionEnd(ctx context.Context, _ *mcpsdk.CallToolRequest, a sessionEndArgs) (*mcpsdk.CallToolResult, any, error) {
	status := session.Status(a.Status)
	if status == "" {
		status = session.StatusOK
	}
	if err := s.cfg.Sessions.Close(ctx, session.CloseInput{
		ID: a.SessionID, Summary: a.Summary, Reflection: a.Reflection,
		Status: status, OutcomeTags: a.OutcomeTags,
	}); err != nil {
		return nil, nil, err
	}
	return textResult("session " + a.SessionID + " closed (" + string(status) + ")")
}

// ---- mnemos_context ----------------------------------------------------

type contextArgs struct {
	Query     string `json:"query,omitempty"`
	Mode      string `json:"mode,omitempty" jsonschema:"empty for query mode, 'recovery' after compaction"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Project   string `json:"project,omitempty"`
	SessionID string `json:"session_id,omitempty" jsonschema:"required for recovery mode"`
	Goal      string `json:"goal,omitempty"`
}

func (s *Server) handleContext(ctx context.Context, _ *mcpsdk.CallToolRequest, a contextArgs) (*mcpsdk.CallToolResult, any, error) {
	if a.Mode == "recovery" {
		if a.SessionID == "" {
			return nil, nil, fmt.Errorf("recovery mode requires session_id")
		}
		if s.cfg.Prewarm == nil {
			return nil, nil, fmt.Errorf("recovery unavailable: prewarm service not wired")
		}
		block, err := s.cfg.Prewarm.Build(ctx, prewarm.Request{
			Mode:      prewarm.ModeCompactionRecovery,
			AgentID:   a.AgentID,
			Project:   a.Project,
			SessionID: a.SessionID,
			Goal:      a.Goal,
			MaxTokens: a.MaxTokens,
		})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(map[string]any{
			"mode":           "recovery",
			"text":           block.Text,
			"token_estimate": block.TokenEstimate,
			"section_count":  len(block.Sections),
			"safety_risk":    block.SafetyReport.MaxRisk.String(),
		})
	}
	if a.Query == "" {
		return nil, nil, fmt.Errorf("query is required when mode != 'recovery'")
	}
	block, err := s.cfg.Memory.Context(ctx, memory.ContextInput{
		Query: a.Query, MaxTokens: a.MaxTokens,
		AgentID: a.AgentID, Project: a.Project,
	})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"text":           block.Text,
		"token_estimate": block.TokenEstimate,
		"observations":   summariseObs(block.Observations),
	})
}

// ---- mnemos_correct ----------------------------------------------------

type correctArgs struct {
	Title          string   `json:"title"`
	Tried          string   `json:"tried"`
	WrongBecause   string   `json:"wrong_because"`
	Fix            string   `json:"fix"`
	TriggerContext string   `json:"trigger_context,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	AgentID        string   `json:"agent_id,omitempty"`
	Project        string   `json:"project,omitempty"`
	SessionID      string   `json:"session_id,omitempty"`
	Importance     int      `json:"importance,omitempty" jsonschema:"defaults to 8 — corrections matter"`
}

func (s *Server) handleCorrect(ctx context.Context, _ *mcpsdk.CallToolRequest, a correctArgs) (*mcpsdk.CallToolResult, any, error) {
	importance := a.Importance
	if importance == 0 {
		importance = 8
	}
	content := fmt.Sprintf("**Tried:** %s\n\n**Wrong because:** %s\n\n**Fix:** %s",
		a.Tried, a.WrongBecause, a.Fix)
	structuredJSON := marshalMap(map[string]string{
		"tried": a.Tried, "wrong_because": a.WrongBecause,
		"fix": a.Fix, "trigger_context": a.TriggerContext,
	})
	res, err := s.cfg.Memory.Save(ctx, memory.SaveInput{
		Title: a.Title, Content: content, Type: memory.TypeCorrection,
		Tags: a.Tags, Importance: importance,
		AgentID: a.AgentID, Project: a.Project, SessionID: a.SessionID,
		Structured: structuredJSON,
	})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{"id": res.Observation.ID, "deduped": res.Deduped})
}

// ---- mnemos_convention -------------------------------------------------

type conventionArgs struct {
	Title     string   `json:"title"`
	Rule      string   `json:"rule"`
	Rationale string   `json:"rationale,omitempty" jsonschema:"the WHY — surfaced in pre-warm"`
	Example   string   `json:"example,omitempty"`
	Project   string   `json:"project"`
	Tags      []string `json:"tags,omitempty"`
	AgentID   string   `json:"agent_id,omitempty"`
}

func (s *Server) handleConvention(ctx context.Context, _ *mcpsdk.CallToolRequest, a conventionArgs) (*mcpsdk.CallToolResult, any, error) {
	content := a.Rule
	if a.Example != "" {
		content += "\n\nExample:\n" + a.Example
	}
	res, err := s.cfg.Memory.Save(ctx, memory.SaveInput{
		Title: a.Title, Content: content, Type: memory.TypeConvention,
		Tags: a.Tags, Project: a.Project, AgentID: a.AgentID,
		Importance: 8, Rationale: a.Rationale,
	})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{"id": res.Observation.ID, "deduped": res.Deduped})
}

// ---- mnemos_touch ------------------------------------------------------

type touchArgs struct {
	Path      string `json:"path"`
	Project   string `json:"project"`
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Note      string `json:"note,omitempty"`
}

func (s *Server) handleTouch(ctx context.Context, _ *mcpsdk.CallToolRequest, a touchArgs) (*mcpsdk.CallToolResult, any, error) {
	if s.cfg.Touches == nil {
		return nil, nil, fmt.Errorf("touch store not wired")
	}
	if err := s.cfg.Touches.Record(ctx, memory.TouchInput{
		Project: a.Project, AgentID: a.AgentID, Path: a.Path,
		SessionID: a.SessionID, Note: a.Note,
	}); err != nil {
		return nil, nil, err
	}
	return textResult("touched " + a.Path)
}

// ---- skills ------------------------------------------------------------

type skillMatchArgs struct {
	Query   string   `json:"query"`
	Tags    []string `json:"tags,omitempty"`
	AgentID string   `json:"agent_id,omitempty"`
	Limit   int      `json:"limit,omitempty"`
}

func (s *Server) handleSkillMatch(ctx context.Context, _ *mcpsdk.CallToolRequest, a skillMatchArgs) (*mcpsdk.CallToolResult, any, error) {
	matches, err := s.cfg.Skills.Match(ctx, skills.MatchInput{
		Query: a.Query, Tags: a.Tags, AgentID: a.AgentID, Limit: a.Limit,
	})
	if err != nil {
		return nil, nil, err
	}
	out := make([]map[string]any, 0, len(matches))
	for _, m := range matches {
		out = append(out, map[string]any{
			"id": m.Skill.ID, "name": m.Skill.Name,
			"description": m.Skill.Description, "procedure": m.Skill.Procedure,
			"pitfalls": m.Skill.Pitfalls, "tags": m.Skill.Tags,
			"use_count": m.Skill.UseCount, "effectiveness": m.Skill.Effectiveness,
			"version": m.Skill.Version, "score": m.Score,
		})
	}
	return jsonResult(map[string]any{"matches": out})
}

type skillSaveArgs struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Procedure      string   `json:"procedure" jsonschema:"numbered steps in markdown"`
	Pitfalls       string   `json:"pitfalls,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	SourceSessions []string `json:"source_sessions,omitempty"`
	AgentID        string   `json:"agent_id,omitempty"`
}

func (s *Server) handleSkillSave(ctx context.Context, _ *mcpsdk.CallToolRequest, a skillSaveArgs) (*mcpsdk.CallToolResult, any, error) {
	sk, err := s.cfg.Skills.Save(ctx, skills.SaveInput{
		AgentID: a.AgentID, Name: a.Name, Description: a.Description,
		Procedure: a.Procedure, Pitfalls: a.Pitfalls, Tags: a.Tags,
		SourceSessions: a.SourceSessions,
	})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{"id": sk.ID, "name": sk.Name, "version": sk.Version})
}

// ---- stats -------------------------------------------------------------

// statsArgs is empty — stats takes no parameters.
type statsArgs struct{}

func (s *Server) handleStats(ctx context.Context, _ *mcpsdk.CallToolRequest, _ statsArgs) (*mcpsdk.CallToolResult, any, error) {
	st, err := s.cfg.Memory.Stats(ctx)
	if err != nil {
		return nil, nil, err
	}
	skillList, _ := s.cfg.Skills.List(ctx, "")
	out := map[string]any{
		"observations":      st.Observations,
		"live_observations": st.LiveObservations,
		"sessions":          st.Sessions,
		"skills":            len(skillList),
		"top_tags":          st.TopTags,
		"recent_sessions":   st.RecentSessions,
		"embedding":         map[string]any{"enabled": s.cfg.Memory.HybridEnabled()},
	}
	if s.cfg.StorageSize != nil {
		if size, err := s.cfg.StorageSize(); err == nil {
			out["storage_bytes"] = size
		}
	}
	if s.cfg.Rumination != nil {
		if c, err := s.cfg.Rumination.Counts(ctx); err == nil {
			out["rumination"] = map[string]any{
				"pending":   c.Pending,
				"resolved":  c.Resolved,
				"dismissed": c.Dismissed,
			}
		}
	}
	return jsonResult(out)
}

// ---- rumination --------------------------------------------------------

type ruminateListArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"max candidates to return; 0 = all"`
}

func (s *Server) handleRuminateList(ctx context.Context, _ *mcpsdk.CallToolRequest, a ruminateListArgs) (*mcpsdk.CallToolResult, any, error) {
	list, err := s.cfg.Rumination.Pending(ctx, a.Limit)
	if err != nil {
		return nil, nil, err
	}
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]any{
			"id":           c.ID,
			"monitor":      c.MonitorName,
			"severity":     c.Severity.String(),
			"reason":       c.Reason,
			"target_kind":  string(c.TargetKind),
			"target_id":    c.TargetID,
			"detected_at":  c.DetectedAt,
			"evidence_n":   len(c.Evidence),
		})
	}
	counts, _ := s.cfg.Rumination.Counts(ctx)
	return jsonResult(map[string]any{
		"candidates": out,
		"counts": map[string]any{
			"pending":   counts.Pending,
			"resolved":  counts.Resolved,
			"dismissed": counts.Dismissed,
		},
	})
}

type ruminatePackArgs struct {
	ID string `json:"id"`
}

func (s *Server) handleRuminatePack(ctx context.Context, _ *mcpsdk.CallToolRequest, a ruminatePackArgs) (*mcpsdk.CallToolResult, any, error) {
	if a.ID == "" {
		return nil, nil, fmt.Errorf("rumination: id required")
	}
	block, err := s.cfg.Rumination.PackByID(ctx, a.ID)
	if err != nil {
		if errors.Is(err, rumination.ErrNotFound) {
			return nil, nil, fmt.Errorf("rumination candidate not found: %s", a.ID)
		}
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"candidate_id":    block.CandidateID,
		"target_kind":     string(block.Target.Kind),
		"target_id":       block.Target.ID,
		"target_name":     block.Target.Name,
		"text":            block.Text,
		"token_estimate":  block.TokenEstimate,
	})
}

type ruminateResolveArgs struct {
	ID         string `json:"id"`
	ResolvedBy string `json:"resolved_by" jsonschema:"ID of the new skill version or superseding observation"`
	// WhyBetter is the Popper-grade guard: name one prediction the revision
	// makes that the old version did not. The schema enforces the field;
	// the handler enforces non-empty and a minimum length.
	WhyBetter string `json:"why_better" jsonschema:"one sentence — a concrete new prediction the revised version makes that the old one did not. Cosmetic rewording is rejected."`
}

func (s *Server) handleRuminateResolve(ctx context.Context, _ *mcpsdk.CallToolRequest, a ruminateResolveArgs) (*mcpsdk.CallToolResult, any, error) {
	if a.ID == "" {
		return nil, nil, fmt.Errorf("rumination: id required")
	}
	if a.ResolvedBy == "" {
		return nil, nil, fmt.Errorf("rumination: resolved_by required — the revision must carry provenance")
	}
	// Minimum length on why_better rejects single-word filler like "yes"
	// or "better" that would satisfy a naive non-empty check. 16 chars is
	// long enough to require an actual sentence without punishing the
	// agent on terseness.
	if len([]rune(a.WhyBetter)) < 16 {
		return nil, nil, fmt.Errorf("rumination: why_better must state a new prediction (min 16 chars), got %d", len([]rune(a.WhyBetter)))
	}

	if err := s.cfg.Rumination.Resolve(ctx, a.ID, a.ResolvedBy); err != nil {
		if errors.Is(err, rumination.ErrNotFound) {
			return nil, nil, fmt.Errorf("rumination candidate not found: %s", a.ID)
		}
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"id":          a.ID,
		"status":      string(rumination.StatusResolved),
		"resolved_by": a.ResolvedBy,
		"why_better":  a.WhyBetter,
	})
}

type ruminateDismissArgs struct {
	ID     string `json:"id"`
	Reason string `json:"reason" jsonschema:"one-line justification — preserved so a later pass does not re-raise the same flag without context"`
}

func (s *Server) handleRuminateDismiss(ctx context.Context, _ *mcpsdk.CallToolRequest, a ruminateDismissArgs) (*mcpsdk.CallToolResult, any, error) {
	if a.ID == "" {
		return nil, nil, fmt.Errorf("rumination: id required")
	}
	if len([]rune(a.Reason)) < 8 {
		return nil, nil, fmt.Errorf("rumination: reason must be at least 8 chars")
	}
	if err := s.cfg.Rumination.Dismiss(ctx, a.ID, a.Reason); err != nil {
		if errors.Is(err, rumination.ErrNotFound) {
			return nil, nil, fmt.Errorf("rumination candidate not found: %s", a.ID)
		}
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"id":     a.ID,
		"status": string(rumination.StatusDismissed),
		"reason": a.Reason,
	})
}

// ---- helpers -----------------------------------------------------------

func summariseObs(obs []memory.Observation) []map[string]any {
	out := make([]map[string]any, 0, len(obs))
	for _, o := range obs {
		out = append(out, map[string]any{
			"id": o.ID, "title": o.Title,
			"type": string(o.Type), "importance": o.Importance,
		})
	}
	return out
}

func parseTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("parse time %q: %w", s, err)
	}
	return &t, nil
}

func marshalMap(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}
