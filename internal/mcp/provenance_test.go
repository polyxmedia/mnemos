package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSaveSurfacesProvenanceFields(t *testing.T) {
	h := newHarness(t)

	_, saveText := h.call("mnemos_save", map[string]any{
		"title":       "fetched from the web",
		"content":     "this came from a browse tool, not verified",
		"type":        "context",
		"source_kind": "tool",
		"trust_tier":  "raw",
	})
	var saved map[string]any
	if err := json.Unmarshal([]byte(saveText), &saved); err != nil {
		t.Fatalf("save result: %v (raw: %s)", err, saveText)
	}
	if saved["source_kind"] != "tool" {
		t.Errorf("save response should echo source_kind=tool, got %v", saved["source_kind"])
	}
	if saved["trust_tier"] != "raw" {
		t.Errorf("save response should echo trust_tier=raw, got %v", saved["trust_tier"])
	}
}

func TestSearchExcludesRawByDefault(t *testing.T) {
	h := newHarness(t)

	h.call("mnemos_save", map[string]any{
		"title":   "curated obs",
		"content": "pgvector is the right choice",
		"type":    "decision",
	})
	h.call("mnemos_save", map[string]any{
		"title":       "raw obs",
		"content":     "pgvector vs chroma — some blog said",
		"type":        "context",
		"source_kind": "tool",
		"trust_tier":  "raw",
	})

	_, out := h.call("mnemos_search", map[string]any{"query": "pgvector"})
	if !strings.Contains(out, "curated obs") {
		t.Errorf("curated must be surfaced, got: %s", out)
	}
	if strings.Contains(out, "raw obs") {
		t.Errorf("raw must be excluded from default search, got: %s", out)
	}

	_, outRaw := h.call("mnemos_search", map[string]any{
		"query": "pgvector", "include_raw": true,
	})
	if !strings.Contains(outRaw, "raw obs") {
		t.Errorf("include_raw=true must surface raw, got: %s", outRaw)
	}
}

func TestSearchIncludesProvenanceInHits(t *testing.T) {
	h := newHarness(t)
	h.call("mnemos_save", map[string]any{
		"title": "check this", "content": "zsh pipeline", "type": "pattern",
	})
	_, out := h.call("mnemos_search", map[string]any{"query": "zsh"})
	// jsonResult emits pretty-printed JSON (two-space indent), so assert
	// against the key itself rather than a packed "k:v" substring.
	for _, field := range []string{`"source_kind"`, `"trust_tier"`, `"derived_from"`} {
		if !strings.Contains(out, field) {
			t.Errorf("search result should include %s, got: %s", field, out)
		}
	}
	if !strings.Contains(out, `"user"`) || !strings.Contains(out, `"curated"`) {
		t.Errorf("default save must carry source_kind=user and trust_tier=curated, got: %s", out)
	}
}

func TestPromoteMovesRawToCurated(t *testing.T) {
	h := newHarness(t)

	_, saveText := h.call("mnemos_save", map[string]any{
		"title":      "quarantined",
		"content":    "raw tool output",
		"type":       "context",
		"trust_tier": "raw",
	})
	var saved map[string]any
	_ = json.Unmarshal([]byte(saveText), &saved)
	id := saved["id"].(string)

	_, promoteText := h.call("mnemos_promote", map[string]any{
		"id":         id,
		"to_tier":    "curated",
		"why_better": "user reviewed this and it matches the codebase",
	})
	if !strings.Contains(promoteText, "curated") {
		t.Errorf("promote response should confirm curated, got: %s", promoteText)
	}

	// Now it should surface in default search.
	_, out := h.call("mnemos_search", map[string]any{"query": "raw tool"})
	if !strings.Contains(out, id) {
		t.Errorf("promoted observation should surface in default search, got: %s", out)
	}
}

func TestPromoteRejectsThinReasonFromMCP(t *testing.T) {
	h := newHarness(t)
	_, saveText := h.call("mnemos_save", map[string]any{
		"title": "x", "content": "y", "type": "context", "trust_tier": "raw",
	})
	var saved map[string]any
	_ = json.Unmarshal([]byte(saveText), &saved)

	res, _ := h.call("mnemos_promote", map[string]any{
		"id": saved["id"], "to_tier": "curated", "why_better": "ok",
	})
	if res == nil || !res.IsError {
		t.Error("promote with thin reason must surface as an MCP error")
	}
}
