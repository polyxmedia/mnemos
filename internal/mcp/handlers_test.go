package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Exercises the handler paths the earlier suites didn't hit.

func TestDeleteWithUnknownIDReturnsError(t *testing.T) {
	h := newHarness(t)
	res, err := h.client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "mnemos_delete",
		Arguments: map[string]any{"id": "nonexistent"},
	})
	if err == nil && !res.IsError {
		t.Error("expected delete to fail on unknown id")
	}
}

func TestSessionEndClosesWithSummary(t *testing.T) {
	h := newHarness(t)
	_, startText := h.call("mnemos_session_start", map[string]any{
		"project": "p", "goal": "g",
	})
	var started map[string]any
	_ = json.Unmarshal([]byte(startText), &started)
	sessID := started["session_id"].(string)

	_, text := h.call("mnemos_session_end", map[string]any{
		"session_id":   sessID,
		"summary":      "done",
		"reflection":   "learned",
		"status":       "ok",
		"outcome_tags": []string{"shipped"},
	})
	if !strings.Contains(text, "closed (ok)") {
		t.Errorf("session end should confirm closure with status: %s", text)
	}
}

func TestSkillSaveAndMatchViaMCP(t *testing.T) {
	h := newHarness(t)
	_, saveText := h.call("mnemos_skill_save", map[string]any{
		"name":        "wire-mcp-tool",
		"description": "add an MCP tool",
		"procedure":   "1. define\n2. register\n3. test",
		"tags":        []string{"mcp"},
	})
	var saved map[string]any
	_ = json.Unmarshal([]byte(saveText), &saved)
	if saved["id"] == nil || saved["version"] == nil {
		t.Errorf("skill save should return id+version: %v", saved)
	}

	_, matchText := h.call("mnemos_skill_match", map[string]any{
		"query": "mcp tool",
	})
	if !strings.Contains(matchText, "wire-mcp-tool") {
		t.Errorf("skill match should find saved skill: %s", matchText)
	}
}

func TestLinkUnknownTypeRejected(t *testing.T) {
	h := newHarness(t)

	// Save two observations to link.
	_, t1 := h.call("mnemos_save", map[string]any{
		"title": "a", "content": "a", "type": "context",
	})
	var o1 map[string]any
	_ = json.Unmarshal([]byte(t1), &o1)

	_, t2 := h.call("mnemos_save", map[string]any{
		"title": "b", "content": "b", "type": "context",
	})
	var o2 map[string]any
	_ = json.Unmarshal([]byte(t2), &o2)

	res, err := h.client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "mnemos_link",
		Arguments: map[string]any{
			"source_id": o1["id"], "target_id": o2["id"],
			"link_type": "bogus",
		},
	})
	if err == nil && !res.IsError {
		t.Error("bogus link type should fail")
	}
}

func TestContextWithoutQueryOrRecoveryErrors(t *testing.T) {
	h := newHarness(t)
	res, err := h.client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "mnemos_context",
		Arguments: map[string]any{}, // neither mode nor query
	})
	if err == nil && !res.IsError {
		t.Error("context with no query/mode should fail")
	}
}

func TestRecoveryRequiresSessionID(t *testing.T) {
	h := newHarness(t)
	res, err := h.client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "mnemos_context",
		Arguments: map[string]any{
			"mode": "recovery", // missing session_id
		},
	})
	if err == nil && !res.IsError {
		t.Error("recovery without session_id should fail")
	}
}

func TestTouchWithoutTouchesStore(t *testing.T) {
	// Not directly testable without rewiring the harness; skip.
	t.Skip("touches store is always wired in harness; covered by TestTouchAppearsInStats")
}
