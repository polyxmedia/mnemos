package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/session"
)

// registerResources attaches the three Mnemos MCP resources:
//
//	mnemos://session/current  — most recent open session
//	mnemos://skills/index     — slim skill index
//	mnemos://stats            — system statistics
func (s *Server) registerResources() {
	s.sdk.AddResource(&mcpsdk.Resource{
		URI:         "mnemos://session/current",
		Name:        "Current session",
		Description: "Most recent open session with goal and project",
		MIMEType:    "application/json",
	}, s.readCurrentSession)

	s.sdk.AddResource(&mcpsdk.Resource{
		URI:         "mnemos://skills/index",
		Name:        "Skills index",
		Description: "All skills (name + description + effectiveness)",
		MIMEType:    "application/json",
	}, s.readSkillsIndex)

	s.sdk.AddResource(&mcpsdk.Resource{
		URI:         "mnemos://stats",
		Name:        "Stats",
		Description: "Memory system statistics",
		MIMEType:    "application/json",
	}, s.readStats)
}

func (s *Server) readCurrentSession(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	sess, err := s.cfg.Sessions.Current(ctx, "")
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return resourceJSON(req.Params.URI, map[string]any{"session": nil})
		}
		return nil, fmt.Errorf("current session: %w", err)
	}
	return resourceJSON(req.Params.URI, sess)
}

func (s *Server) readSkillsIndex(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	list, err := s.cfg.Skills.List(ctx, "")
	if err != nil {
		return nil, err
	}
	slim := make([]map[string]any, 0, len(list))
	for _, sk := range list {
		slim = append(slim, map[string]any{
			"id":            sk.ID,
			"name":          sk.Name,
			"description":   sk.Description,
			"tags":          sk.Tags,
			"use_count":     sk.UseCount,
			"effectiveness": sk.Effectiveness,
			"version":       sk.Version,
		})
	}
	return resourceJSON(req.Params.URI, map[string]any{"skills": slim})
}

func (s *Server) readStats(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	st, err := s.cfg.Memory.Stats(ctx)
	if err != nil {
		return nil, err
	}
	return resourceJSON(req.Params.URI, st)
}

func resourceJSON(uri string, v any) (*mcpsdk.ReadResourceResult, error) {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal resource: %w", err)
	}
	return &mcpsdk.ReadResourceResult{
		Contents: []*mcpsdk.ResourceContents{{
			URI:      uri,
			MIMEType: "application/json",
			Text:     string(buf),
		}},
	}, nil
}
