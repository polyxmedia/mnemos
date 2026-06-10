package mcp_test

import (
	"strings"
	"testing"
)

func TestPremortemSurfacesMatchingCorrection(t *testing.T) {
	h := newHarness(t)

	h.call("mnemos_correct", map[string]any{
		"title":           "oauth 401 retry is wrong",
		"tried":           "retry oauth requests on 401",
		"wrong_because":   "401 is an auth failure, not transient",
		"fix":             "refresh the token then retry once",
		"trigger_context": "oauth 401 handling in api clients",
		"project":         "api",
	})

	_, text := h.call("mnemos_premortem", map[string]any{
		"plan":    "add oauth 401 retry handling to the api client",
		"project": "api",
	})
	if !strings.Contains(text, "how similar attempts failed") {
		t.Errorf("premortem missing failure section: %s", text)
	}
	if !strings.Contains(text, "401") {
		t.Errorf("premortem missing the matching correction: %s", text)
	}
}

func TestPremortemEmptyStoreReturnsVerdict(t *testing.T) {
	h := newHarness(t)

	_, text := h.call("mnemos_premortem", map[string]any{
		"plan": "do something nothing in the store matches",
	})
	if !strings.Contains(text, "no recorded failures") {
		t.Errorf("expected proceed verdict on empty store, got: %s", text)
	}
}
