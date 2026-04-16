package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
)

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
	Importance     int      `json:"importance,omitempty"`
}

func (s *Server) toolCorrect() toolDef {
	return toolDef{
		tool: Tool{
			Name: "mnemos_correct",
			Description: "Record a correction: an approach that was tried, why it was wrong, and the fix. " +
				"Corrections are weighted higher in retrieval than regular observations because preventing repeat mistakes " +
				"is the highest-leverage use of memory. Call this after discovering a wrong approach and its fix.",
			InputSchema: mustSchema(`{
			"type":"object",
			"required":["title","tried","wrong_because","fix"],
			"properties":{
				"title":           {"type":"string","description":"short scannable label (e.g. 'oauth retry without backoff')"},
				"tried":           {"type":"string","description":"the approach that was attempted"},
				"wrong_because":   {"type":"string","description":"why it failed — the insight"},
				"fix":             {"type":"string","description":"the approach that works"},
				"trigger_context": {"type":"string","description":"situation where this correction applies (used for future matching)"},
				"tags":            {"type":"array","items":{"type":"string"}},
				"agent_id":        {"type":"string"},
				"project":         {"type":"string"},
				"session_id":      {"type":"string"},
				"importance":      {"type":"integer","minimum":1,"maximum":10,"description":"defaults to 8 — corrections matter"}
			}
		}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a correctArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			importance := a.Importance
			if importance == 0 {
				importance = 8
			}
			content := fmt.Sprintf(
				"**Tried:** %s\n\n**Wrong because:** %s\n\n**Fix:** %s",
				a.Tried, a.WrongBecause, a.Fix,
			)
			structured := map[string]string{
				"tried":           a.Tried,
				"wrong_because":   a.WrongBecause,
				"fix":             a.Fix,
				"trigger_context": a.TriggerContext,
			}
			structuredJSON, _ := json.Marshal(structured)

			res, err := s.mem.Save(ctx, memory.SaveInput{
				Title:      a.Title,
				Content:    content,
				Type:       memory.TypeCorrection,
				Tags:       a.Tags,
				Importance: importance,
				AgentID:    a.AgentID,
				Project:    a.Project,
				SessionID:  a.SessionID,
				Structured: string(structuredJSON),
			})
			if err != nil {
				return nil, err
			}
			return jsonResult(map[string]any{
				"id":      res.Observation.ID,
				"deduped": res.Deduped,
			})
		},
	}
}

// ---- mnemos_convention -------------------------------------------------

type conventionArgs struct {
	Title     string   `json:"title"`
	Rule      string   `json:"rule"`
	Rationale string   `json:"rationale,omitempty"`
	Example   string   `json:"example,omitempty"`
	Project   string   `json:"project"`
	Tags      []string `json:"tags,omitempty"`
	AgentID   string   `json:"agent_id,omitempty"`
}

func (s *Server) toolConvention() toolDef {
	return toolDef{
		tool: Tool{
			Name: "mnemos_convention",
			Description: "Declare a project convention (coding style, naming, architecture rule). " +
				"Conventions are auto-injected at session_start for the matching project, so the agent " +
				"never has to re-derive them by reading the codebase. Declared once, applied forever.",
			InputSchema: mustSchema(`{
			"type":"object",
			"required":["title","rule","project"],
			"properties":{
				"title":     {"type":"string","description":"short name (e.g. 'error wrapping')"},
				"rule":      {"type":"string","description":"the rule itself"},
				"rationale": {"type":"string","description":"WHY this rule exists — crucial for future edge cases"},
				"example":   {"type":"string","description":"optional code snippet showing the rule applied"},
				"project":   {"type":"string","description":"project this convention applies to"},
				"tags":      {"type":"array","items":{"type":"string"}},
				"agent_id":  {"type":"string"}
			}
		}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a conventionArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			var content string
			if a.Example != "" {
				content = a.Rule + "\n\nExample:\n" + a.Example
			} else {
				content = a.Rule
			}
			res, err := s.mem.Save(ctx, memory.SaveInput{
				Title:      a.Title,
				Content:    content,
				Type:       memory.TypeConvention,
				Tags:       a.Tags,
				Project:    a.Project,
				AgentID:    a.AgentID,
				Importance: 8,
				Rationale:  a.Rationale,
			})
			if err != nil {
				return nil, err
			}
			return jsonResult(map[string]any{
				"id":      res.Observation.ID,
				"deduped": res.Deduped,
			})
		},
	}
}

// ---- mnemos_touch ------------------------------------------------------

type touchArgs struct {
	Path      string `json:"path"`
	Project   string `json:"project"`
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Note      string `json:"note,omitempty"`
}

func (s *Server) toolTouch() toolDef {
	return toolDef{
		tool: Tool{
			Name: "mnemos_touch",
			Description: "Record that a file was touched in the current session. Builds a heat map: " +
				"frequently-touched files get priority in session pre-warming so the agent focuses on " +
				"what actually matters instead of boilerplate. Optional note captures why it was touched.",
			InputSchema: mustSchema(`{
			"type":"object",
			"required":["path","project"],
			"properties":{
				"path":       {"type":"string"},
				"project":    {"type":"string"},
				"session_id": {"type":"string"},
				"agent_id":   {"type":"string"},
				"note":       {"type":"string"}
			}
		}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			if s.touches == nil {
				return nil, fmt.Errorf("touch store not wired")
			}
			var a touchArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			if err := s.touches.Record(ctx, memory.TouchInput{
				Project:   a.Project,
				AgentID:   a.AgentID,
				Path:      a.Path,
				SessionID: a.SessionID,
				Note:      a.Note,
			}); err != nil {
				return nil, err
			}
			return textResult("touched " + a.Path)
		},
	}
}

// ---- mnemos_link -------------------------------------------------------

type linkArgs struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	LinkType string `json:"link_type"`
}

func (s *Server) toolLink() toolDef {
	return toolDef{
		tool: Tool{
			Name: "mnemos_link",
			Description: "Link two observations: related | caused_by | supersedes | contradicts | refines. " +
				"'supersedes' automatically invalidates the target (bi-temporal: old fact preserved, new fact active). " +
				"Use this for 'we used to do X, now we do Y' to avoid context poisoning from stale facts.",
			InputSchema: mustSchema(`{
			"type":"object",
			"required":["source_id","target_id","link_type"],
			"properties":{
				"source_id": {"type":"string"},
				"target_id": {"type":"string"},
				"link_type": {"type":"string","enum":["related","caused_by","supersedes","contradicts","refines"]}
			}
		}`),
		},
		handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a linkArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			if err := s.mem.Link(ctx, a.SourceID, a.TargetID, memory.LinkType(a.LinkType)); err != nil {
				return nil, err
			}
			return textResult(fmt.Sprintf("linked %s -[%s]-> %s", a.SourceID, a.LinkType, a.TargetID))
		},
	}
}

// ---- pre-warming for session_start + compaction recovery --------------

// buildPrewarm is called by the session_start and context tools to produce
// a pushable context block. Nil-safe: returns nil block if prewarm service
// wasn't wired.
func (s *Server) buildPrewarm(ctx context.Context, req prewarm.Request) *prewarm.Block {
	if s.prewarm == nil {
		return nil
	}
	block, err := s.prewarm.Build(ctx, req)
	if err != nil {
		s.log.Warn("prewarm failed", "err", err)
		return nil
	}
	return block
}
