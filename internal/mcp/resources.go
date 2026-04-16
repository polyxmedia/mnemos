package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/polyxmedia/mnemos/internal/session"
)

func (s *Server) resourceList() []Resource {
	return []Resource{
		{
			URI:         "mnemos://session/current",
			Name:        "Current session",
			Description: "The most recently opened session with its goal and recent observations",
			MIMEType:    "application/json",
		},
		{
			URI:         "mnemos://skills/index",
			Name:        "Skills index",
			Description: "All skills (name + description + effectiveness) for the default agent",
			MIMEType:    "application/json",
		},
		{
			URI:         "mnemos://stats",
			Name:        "Stats",
			Description: "Memory system statistics",
			MIMEType:    "application/json",
		},
	}
}

func (s *Server) handleResourceRead(ctx context.Context, req rpcRequest) *rpcResponse {
	var params resourcesReadParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, errInvalidParams, "invalid resource read params", err.Error())
	}
	text, mime, err := s.readResource(ctx, params.URI)
	if err != nil {
		return errorResponse(req.ID, errInternal, err.Error(), nil)
	}
	return successResponse(req.ID, resourcesReadResult{
		Contents: []resourceContent{{URI: params.URI, MIMEType: mime, Text: text}},
	})
}

func (s *Server) readResource(ctx context.Context, uri string) (string, string, error) {
	switch uri {
	case "mnemos://session/current":
		sess, err := s.sess.Current(ctx, "")
		if err != nil {
			if errors.Is(err, session.ErrNotFound) {
				return `{"session":null}`, "application/json", nil
			}
			return "", "", fmt.Errorf("current session: %w", err)
		}
		return marshal(sess)
	case "mnemos://skills/index":
		list, err := s.skill.List(ctx, "")
		if err != nil {
			return "", "", err
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
		return marshal(map[string]any{"skills": slim})
	case "mnemos://stats":
		st, err := s.mem.Stats(ctx)
		if err != nil {
			return "", "", err
		}
		return marshal(st)
	default:
		return "", "", fmt.Errorf("unknown resource: %s", uri)
	}
}

func marshal(v any) (string, string, error) {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("marshal: %w", err)
	}
	return string(buf), "application/json", nil
}
