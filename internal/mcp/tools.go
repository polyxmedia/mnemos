package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// toolHandler executes one tool. The result is already a ToolResult so the
// router can marshal it directly.
type toolHandler func(ctx context.Context, args json.RawMessage) (*ToolResult, error)

// toolDef bundles a tool's descriptor with its handler.
type toolDef struct {
	tool    Tool
	handler toolHandler
}

func (s *Server) registerTools() {
	defs := []toolDef{
		s.toolSave(),
		s.toolSearch(),
		s.toolGet(),
		s.toolDelete(),
		s.toolSessionStart(),
		s.toolSessionEnd(),
		s.toolContext(),
		s.toolSkillMatch(),
		s.toolSkillSave(),
		s.toolStats(),
	}
	s.handlers = make(map[string]toolHandler, len(defs))
	s.toolOrder = make([]string, 0, len(defs))
	for _, d := range defs {
		s.handlers[d.tool.Name] = d.handler
		s.toolOrder = append(s.toolOrder, d.tool.Name)
	}
	s.toolDescriptors = make(map[string]Tool, len(defs))
	for _, d := range defs {
		s.toolDescriptors[d.tool.Name] = d.tool
	}
}

func (s *Server) toolList() []Tool {
	out := make([]Tool, 0, len(s.toolOrder))
	for _, name := range s.toolOrder {
		out = append(out, s.toolDescriptors[name])
	}
	return out
}

func (s *Server) handleToolCall(ctx context.Context, req rpcRequest) *rpcResponse {
	var params toolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, errInvalidParams, "invalid tool params", err.Error())
	}
	handler, ok := s.handlers[params.Name]
	if !ok {
		return errorResponse(req.ID, errMethodNotFound, "unknown tool: "+params.Name, nil)
	}
	result, err := handler(ctx, params.Arguments)
	if err != nil {
		s.log.Warn("tool failed", "tool", params.Name, "err", err)
		return successResponse(req.ID, &ToolResult{
			Content: []ContentBlock{textBlock("error: " + err.Error())},
			IsError: true,
		})
	}
	return successResponse(req.ID, result)
}

// ---- mnemos_save ----

type saveArgs struct {
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Type       string   `json:"type"`
	Tags       []string `json:"tags,omitempty"`
	Importance int      `json:"importance,omitempty"`
	TTLDays    int      `json:"ttl_days,omitempty"`
	AgentID    string   `json:"agent_id,omitempty"`
	Project    string   `json:"project,omitempty"`
	SessionID  string   `json:"session_id,omitempty"`
	ValidFrom  string   `json:"valid_from,omitempty"`
	ValidUntil string   `json:"valid_until,omitempty"`
}

func (s *Server) toolSave() toolDef {
	return toolDef{
		tool: Tool{
			Name: "mnemos_save",
			Description: "Persist an agent-curated observation (decision, bugfix, pattern, preference, context, architecture, episodic, semantic, procedural). " +
				"Mnemos is deliberately agent-curated: save only things worth remembering later. Returns the observation ID.",
			InputSchema: mustSchema(`{
			"type": "object",
			"required": ["title","content","type"],
			"properties": {
				"title":      {"type":"string","description":"short, scannable label"},
				"content":    {"type":"string","description":"the memory itself: structure it as what/why/where/learned"},
				"type":       {"type":"string","enum":["decision","bugfix","pattern","preference","context","architecture","episodic","semantic","procedural"]},
				"tags":       {"type":"array","items":{"type":"string"}},
				"importance": {"type":"integer","minimum":1,"maximum":10,"description":"1..10, defaults to 5"},
				"ttl_days":   {"type":"integer","minimum":1,"description":"optional auto-expiry"},
				"agent_id":   {"type":"string"},
				"project":    {"type":"string"},
				"session_id": {"type":"string"},
				"valid_from": {"type":"string","format":"date-time"},
				"valid_until":{"type":"string","format":"date-time"}
			}
		}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a saveArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("parse args: %w", err)
			}
			in := memory.SaveInput{
				Title:      a.Title,
				Content:    a.Content,
				Type:       memory.ObsType(a.Type),
				Tags:       a.Tags,
				Importance: a.Importance,
				TTLDays:    a.TTLDays,
				AgentID:    a.AgentID,
				Project:    a.Project,
				SessionID:  a.SessionID,
			}
			if t, err := parseTime(a.ValidFrom); err != nil {
				return nil, err
			} else if t != nil {
				in.ValidFrom = t
			}
			if t, err := parseTime(a.ValidUntil); err != nil {
				return nil, err
			} else if t != nil {
				in.ValidUntil = t
			}

			o, err := s.mem.Save(ctx, in)
			if err != nil {
				return nil, err
			}
			return jsonResult(map[string]any{
				"id":         o.ID,
				"title":      o.Title,
				"type":       string(o.Type),
				"created_at": o.CreatedAt,
			})
		},
	}
}

// ---- mnemos_search ----

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

func (s *Server) toolSearch() toolDef {
	return toolDef{
		tool: Tool{
			Name: "mnemos_search",
			Description: "Full-text search over observations. Ranks by BM25 + recency + access frequency + importance. " +
				"Default scope: valid-now (excludes invalidated / expired). Returns compact hits (id, title, snippet, score). " +
				"Use mnemos_get for full content.",
			InputSchema: mustSchema(`{
			"type":"object",
			"required":["query"],
			"properties":{
				"query":         {"type":"string"},
				"type":          {"type":"string"},
				"tags":          {"type":"array","items":{"type":"string"}},
				"min_importance":{"type":"integer","minimum":1,"maximum":10},
				"limit":         {"type":"integer","minimum":1,"maximum":100},
				"agent_id":      {"type":"string"},
				"project":       {"type":"string"},
				"include_stale": {"type":"boolean","description":"include invalidated/expired observations"},
				"as_of":         {"type":"string","format":"date-time","description":"historical query: what was valid at this time"}
			}
		}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a searchArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			in := memory.SearchInput{
				Query:         a.Query,
				Type:          memory.ObsType(a.Type),
				Tags:          a.Tags,
				MinImportance: a.MinImportance,
				Limit:         a.Limit,
				AgentID:       a.AgentID,
				Project:       a.Project,
				IncludeStale:  a.IncludeStale,
			}
			if t, err := parseTime(a.AsOf); err != nil {
				return nil, err
			} else if t != nil {
				in.AsOf = *t
			}
			results, err := s.mem.Search(ctx, in)
			if err != nil {
				return nil, err
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
		},
	}
}

// ---- mnemos_get ----

type idArgs struct {
	ID string `json:"id"`
}

func (s *Server) toolGet() toolDef {
	return toolDef{
		tool: Tool{
			Name:        "mnemos_get",
			Description: "Fetch an observation by ID. Returns full content; bumps the access counter.",
			InputSchema: mustSchema(`{"type":"object","required":["id"],"properties":{"id":{"type":"string"}}}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a idArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			o, err := s.mem.Get(ctx, a.ID)
			if err != nil {
				if errors.Is(err, memory.ErrNotFound) {
					return nil, fmt.Errorf("observation not found: %s", a.ID)
				}
				return nil, err
			}
			return jsonResult(o)
		},
	}
}

// ---- mnemos_delete ----

func (s *Server) toolDelete() toolDef {
	return toolDef{
		tool: Tool{
			Name: "mnemos_delete",
			Description: "Hard-delete an observation. Use only for mistaken saves. For facts that changed, use supersession via mnemos_save + link (internal) or mark stale via the CLI.",
			InputSchema: mustSchema(`{"type":"object","required":["id"],"properties":{"id":{"type":"string"}}}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a idArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			if err := s.mem.Delete(ctx, a.ID); err != nil {
				return nil, err
			}
			return textResult("deleted " + a.ID)
		},
	}
}

// ---- mnemos_session_start ----

type sessionStartArgs struct {
	AgentID string `json:"agent_id,omitempty"`
	Project string `json:"project,omitempty"`
	Goal    string `json:"goal,omitempty"`
}

func (s *Server) toolSessionStart() toolDef {
	return toolDef{
		tool: Tool{
			Name:        "mnemos_session_start",
			Description: "Open a new session. Observations saved during a session get linked to it for provenance. Call at the start of a work unit.",
			InputSchema: mustSchema(`{
			"type":"object",
			"properties":{
				"agent_id":{"type":"string"},
				"project": {"type":"string"},
				"goal":    {"type":"string","description":"what you're setting out to do"}
			}
		}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a sessionStartArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			sess, err := s.sess.Open(ctx, session.OpenInput{
				AgentID: a.AgentID, Project: a.Project, Goal: a.Goal,
			})
			if err != nil {
				return nil, err
			}
			return jsonResult(map[string]any{
				"session_id": sess.ID,
				"started_at": sess.StartedAt,
			})
		},
	}
}

// ---- mnemos_session_end ----

type sessionEndArgs struct {
	SessionID  string `json:"session_id"`
	Summary    string `json:"summary"`
	Reflection string `json:"reflection,omitempty"`
}

func (s *Server) toolSessionEnd() toolDef {
	return toolDef{
		tool: Tool{
			Name: "mnemos_session_end",
			Description: "Close a session with summary and optional reflection. Reflection is the agent-authored extraction of transferable lessons — this is what gets promoted into skills during consolidation.",
			InputSchema: mustSchema(`{
			"type":"object",
			"required":["session_id","summary"],
			"properties":{
				"session_id":{"type":"string"},
				"summary":   {"type":"string","description":"what shipped, what broke, what was learned"},
				"reflection":{"type":"string","description":"transferable lessons: patterns, sequences, pitfalls"}
			}
		}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a sessionEndArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			if err := s.sess.Close(ctx, session.CloseInput{
				ID: a.SessionID, Summary: a.Summary, Reflection: a.Reflection,
			}); err != nil {
				return nil, err
			}
			return textResult("session " + a.SessionID + " closed")
		},
	}
}

// ---- mnemos_context ----

type contextArgs struct {
	Query     string `json:"query"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Project   string `json:"project,omitempty"`
}

func (s *Server) toolContext() toolDef {
	return toolDef{
		tool: Tool{
			Name: "mnemos_context",
			Description: "Return a token-budgeted block of relevant memory ready for injection. Safer than search+get loops: the block never exceeds max_tokens (default 2000).",
			InputSchema: mustSchema(`{
			"type":"object",
			"required":["query"],
			"properties":{
				"query":     {"type":"string"},
				"max_tokens":{"type":"integer","minimum":100,"maximum":20000},
				"agent_id":  {"type":"string"},
				"project":   {"type":"string"}
			}
		}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a contextArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			block, err := s.mem.Context(ctx, memory.ContextInput{
				Query: a.Query, MaxTokens: a.MaxTokens,
				AgentID: a.AgentID, Project: a.Project,
			})
			if err != nil {
				return nil, err
			}
			return jsonResult(map[string]any{
				"text":            block.Text,
				"token_estimate":  block.TokenEstimate,
				"observations":    summariseObs(block.Observations),
			})
		},
	}
}

// ---- mnemos_skill_match ----

type skillMatchArgs struct {
	Query   string   `json:"query"`
	Tags    []string `json:"tags,omitempty"`
	AgentID string   `json:"agent_id,omitempty"`
	Limit   int      `json:"limit,omitempty"`
}

func (s *Server) toolSkillMatch() toolDef {
	return toolDef{
		tool: Tool{
			Name:        "mnemos_skill_match",
			Description: "Find skills relevant to a task. Ranking factors in BM25 + effectiveness (skills that worked before rise up).",
			InputSchema: mustSchema(`{
			"type":"object",
			"required":["query"],
			"properties":{
				"query":   {"type":"string"},
				"tags":    {"type":"array","items":{"type":"string"}},
				"agent_id":{"type":"string"},
				"limit":   {"type":"integer","minimum":1,"maximum":20}
			}
		}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a skillMatchArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			matches, err := s.skill.Match(ctx, skills.MatchInput{
				Query: a.Query, Tags: a.Tags, AgentID: a.AgentID, Limit: a.Limit,
			})
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(matches))
			for _, m := range matches {
				out = append(out, map[string]any{
					"id":            m.Skill.ID,
					"name":          m.Skill.Name,
					"description":   m.Skill.Description,
					"procedure":     m.Skill.Procedure,
					"pitfalls":      m.Skill.Pitfalls,
					"tags":          m.Skill.Tags,
					"use_count":     m.Skill.UseCount,
					"effectiveness": m.Skill.Effectiveness,
					"version":       m.Skill.Version,
					"score":         m.Score,
				})
			}
			return jsonResult(map[string]any{"matches": out})
		},
	}
}

// ---- mnemos_skill_save ----

type skillSaveArgs struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Procedure      string   `json:"procedure"`
	Pitfalls       string   `json:"pitfalls,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	SourceSessions []string `json:"source_sessions,omitempty"`
	AgentID        string   `json:"agent_id,omitempty"`
}

func (s *Server) toolSkillSave() toolDef {
	return toolDef{
		tool: Tool{
			Name: "mnemos_skill_save",
			Description: "Save or version a reusable procedure. Keyed by (agent_id, name); saving the same name again bumps the version. Procedure is step-by-step markdown the agent can follow.",
			InputSchema: mustSchema(`{
			"type":"object",
			"required":["name","description","procedure"],
			"properties":{
				"name":           {"type":"string"},
				"description":    {"type":"string"},
				"procedure":      {"type":"string","description":"numbered steps in markdown"},
				"pitfalls":       {"type":"string","description":"known failure modes"},
				"tags":           {"type":"array","items":{"type":"string"}},
				"source_sessions":{"type":"array","items":{"type":"string"}},
				"agent_id":       {"type":"string"}
			}
		}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a skillSaveArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			sk, err := s.skill.Save(ctx, skills.SaveInput{
				AgentID:        a.AgentID,
				Name:           a.Name,
				Description:    a.Description,
				Procedure:      a.Procedure,
				Pitfalls:       a.Pitfalls,
				Tags:           a.Tags,
				SourceSessions: a.SourceSessions,
			})
			if err != nil {
				return nil, err
			}
			return jsonResult(map[string]any{
				"id":      sk.ID,
				"name":    sk.Name,
				"version": sk.Version,
			})
		},
	}
}

// ---- mnemos_stats ----

func (s *Server) toolStats() toolDef {
	return toolDef{
		tool: Tool{
			Name:        "mnemos_stats",
			Description: "Memory system statistics: counts, top tags, recent sessions.",
			InputSchema: mustSchema(`{"type":"object","properties":{}}`),
		},
		handler: func(ctx context.Context, _ json.RawMessage) (*ToolResult, error) {
			st, err := s.mem.Stats(ctx)
			if err != nil {
				return nil, err
			}
			skillCount, _ := s.skill.List(ctx, "")
			return jsonResult(map[string]any{
				"observations":      st.Observations,
				"live_observations": st.LiveObservations,
				"sessions":          st.Sessions,
				"skills":            len(skillCount),
				"top_tags":          st.TopTags,
				"recent_sessions":   st.RecentSessions,
			})
		},
	}
}

// ---- helpers ----

func summariseObs(obs []memory.Observation) []map[string]any {
	out := make([]map[string]any, 0, len(obs))
	for _, o := range obs {
		out = append(out, map[string]any{
			"id":         o.ID,
			"title":      o.Title,
			"type":       string(o.Type),
			"importance": o.Importance,
		})
	}
	return out
}

func textBlock(s string) ContentBlock { return ContentBlock{Type: "text", Text: s} }

func textResult(s string) (*ToolResult, error) {
	return &ToolResult{Content: []ContentBlock{textBlock(s)}}, nil
}

func jsonResult(v any) (*ToolResult, error) {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return &ToolResult{Content: []ContentBlock{textBlock(string(buf))}}, nil
}

func mustSchema(s string) json.RawMessage {
	if !json.Valid([]byte(s)) {
		panic("invalid inline schema: " + s)
	}
	return json.RawMessage(strings.TrimSpace(s))
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
