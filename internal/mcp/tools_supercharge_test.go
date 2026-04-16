package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConventionToolAndAutoInject(t *testing.T) {
	srv := newTestServer(t)
	toolCall(t, srv, "mnemos_convention", map[string]any{
		"title":     "snake case files",
		"rule":      "use snake_case for Go filenames",
		"rationale": "consistent with stdlib",
		"project":   "proj",
	})
	start := toolCall(t, srv, "mnemos_session_start", map[string]any{
		"project": "proj", "goal": "add handler",
	})
	if !strings.Contains(toolResultText(t, start), "snake case files") {
		t.Errorf("convention should surface in session_start prewarm")
	}
}

func TestTouchToolAndHotFilesInPrewarm(t *testing.T) {
	srv := newTestServer(t)
	for _, p := range []string{"a.go", "a.go", "a.go", "b.go"} {
		toolCall(t, srv, "mnemos_touch", map[string]any{
			"path": p, "project": "proj",
		})
	}
	start := toolCall(t, srv, "mnemos_session_start", map[string]any{
		"project": "proj", "goal": "refactor",
	})
	text := toolResultText(t, start)
	if !strings.Contains(text, "a.go") {
		t.Errorf("hot files should surface in prewarm: %s", text)
	}
}

func TestStatsToolIncludesEmbeddingAndStorage(t *testing.T) {
	srv := newTestServer(t)
	resp := toolCall(t, srv, "mnemos_stats", map[string]any{})
	var out map[string]any
	if err := json.Unmarshal([]byte(toolResultText(t, resp)), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["embedding"]; !ok {
		t.Errorf("stats missing embedding section: %v", out)
	}
	// storage_bytes is optional — only present when StorageSize is wired.
	// Here we didn't wire it, so skipped.
}

func TestSessionEndAcceptsStatus(t *testing.T) {
	srv := newTestServer(t)
	startResp := toolCall(t, srv, "mnemos_session_start", map[string]any{
		"project": "p", "goal": "x",
	})
	var started map[string]any
	_ = json.Unmarshal([]byte(toolResultText(t, startResp)), &started)
	sessID := started["session_id"].(string)

	endResp := toolCall(t, srv, "mnemos_session_end", map[string]any{
		"session_id":   sessID,
		"summary":      "didn't work",
		"status":       "failed",
		"outcome_tags": []string{"timeout"},
	})
	if !strings.Contains(toolResultText(t, endResp), "failed") {
		t.Errorf("session end should confirm status: %s", toolResultText(t, endResp))
	}
}
