package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/mcp"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func newTestServer(t *testing.T) *mcp.Server {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return mcp.NewServer(mcp.Config{
		Name:     "mnemos",
		Version:  "test",
		Memory:   memory.NewService(memory.Config{Store: db.Observations()}),
		Sessions: session.NewService(session.Config{Store: db.Sessions()}),
		Skills:   skills.NewService(skills.Config{Store: db.Skills()}),
		Touches:  db.Touches(),
		Prewarm: prewarm.NewService(prewarm.Config{
			Observations: db.Observations(),
			Sessions:     db.Sessions(),
			Skills:       db.Skills(),
			Touches:      db.Touches(),
		}),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// rpcCall encodes a single JSON-RPC request, runs the server on it, and
// returns the decoded response.
func rpcCall(t *testing.T, srv *mcp.Server, id int, method string, params any) map[string]any {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	in, _ := json.Marshal(req)
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), bytes.NewReader(append(in, '\n')), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode: %v (raw: %s)", err, out.String())
	}
	return resp
}

func toolCall(t *testing.T, srv *mcp.Server, name string, args any) map[string]any {
	t.Helper()
	resp := rpcCall(t, srv, 1, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if errObj, ok := resp["error"]; ok && errObj != nil {
		t.Fatalf("tool %s errored: %v", name, errObj)
	}
	return resp
}

func toolResultText(t *testing.T, resp map[string]any) string {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result object: %v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content: %v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] not object")
	}
	return first["text"].(string)
}

func TestInitialize(t *testing.T) {
	srv := newTestServer(t)
	resp := rpcCall(t, srv, 1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
	})
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize failed: %v", resp)
	}
	if result["protocolVersion"] == nil || result["serverInfo"] == nil {
		t.Errorf("missing fields in initialize result: %v", result)
	}
}

func TestToolsListReturnsAllFourteen(t *testing.T) {
	srv := newTestServer(t)
	resp := rpcCall(t, srv, 1, "tools/list", nil)
	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 14 {
		t.Errorf("expected 14 tools, got %d", len(tools))
	}
	expected := []string{
		"mnemos_save", "mnemos_search", "mnemos_get", "mnemos_delete", "mnemos_link",
		"mnemos_session_start", "mnemos_session_end", "mnemos_context",
		"mnemos_skill_match", "mnemos_skill_save", "mnemos_stats",
		"mnemos_correct", "mnemos_convention", "mnemos_touch",
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	for _, want := range expected {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
}

func TestSessionStartReturnsPrewarm(t *testing.T) {
	srv := newTestServer(t)

	// Seed a convention for the project.
	toolCall(t, srv, "mnemos_convention", map[string]any{
		"title":     "error wrapping",
		"rule":      "all errors wrapped with fmt.Errorf using %w",
		"rationale": "preserves error chain for errors.Is",
		"project":   "mnemos",
	})

	// Start a session on that project.
	startResp := toolCall(t, srv, "mnemos_session_start", map[string]any{
		"project": "mnemos",
		"goal":    "add api route",
	})
	text := toolResultText(t, startResp)
	if !strings.Contains(text, "prewarm") {
		t.Errorf("session_start must return prewarm block, got: %s", text)
	}
	if !strings.Contains(text, "error wrapping") {
		t.Errorf("prewarm must include seeded convention, got: %s", text)
	}
}

func TestCorrectionAndLink(t *testing.T) {
	srv := newTestServer(t)

	// Save two related observations.
	r1 := toolCall(t, srv, "mnemos_save", map[string]any{
		"title": "Use X", "content": "we use X", "type": "decision",
	})
	var s1 map[string]any
	_ = json.Unmarshal([]byte(toolResultText(t, r1)), &s1)
	id1 := s1["id"].(string)

	r2 := toolCall(t, srv, "mnemos_save", map[string]any{
		"title": "Use Y", "content": "we use Y now", "type": "decision",
	})
	var s2 map[string]any
	_ = json.Unmarshal([]byte(toolResultText(t, r2)), &s2)
	id2 := s2["id"].(string)

	// Link them — supersedes should invalidate the old one.
	toolCall(t, srv, "mnemos_link", map[string]any{
		"source_id": id2, "target_id": id1, "link_type": "supersedes",
	})

	// Default search should no longer return the old one.
	sResp := toolCall(t, srv, "mnemos_search", map[string]any{"query": "use"})
	body := toolResultText(t, sResp)
	if strings.Contains(body, id1) {
		t.Errorf("superseded observation should be hidden from default search")
	}

	// Correct with structured fields.
	corrResp := toolCall(t, srv, "mnemos_correct", map[string]any{
		"title":         "oauth retry was wrong",
		"tried":         "retry immediately on 401",
		"wrong_because": "401 is auth failure, not transient",
		"fix":           "refresh token, then retry once",
		"project":       "mnemos",
	})
	var corr map[string]any
	_ = json.Unmarshal([]byte(toolResultText(t, corrResp)), &corr)
	if corr["id"] == nil {
		t.Error("correction should return an id")
	}
}

func TestCompactionRecoveryMode(t *testing.T) {
	srv := newTestServer(t)

	startResp := toolCall(t, srv, "mnemos_session_start", map[string]any{
		"project": "mnemos",
		"goal":    "finish prewarm",
	})
	var started map[string]any
	_ = json.Unmarshal([]byte(toolResultText(t, startResp)), &started)
	sessID := started["session_id"].(string)

	toolCall(t, srv, "mnemos_save", map[string]any{
		"title":      "decided push > pull",
		"content":    "session_start returns prewarm block",
		"type":       "decision",
		"session_id": sessID,
		"project":    "mnemos",
	})

	recResp := toolCall(t, srv, "mnemos_context", map[string]any{
		"mode":       "recovery",
		"session_id": sessID,
		"project":    "mnemos",
	})
	text := toolResultText(t, recResp)
	if !strings.Contains(text, "finish prewarm") {
		t.Errorf("recovery must include session goal, got: %s", text)
	}
	if !strings.Contains(text, "decided push") {
		t.Errorf("recovery must include in-session observation, got: %s", text)
	}
}

func TestSaveSearchGetRoundTripViaMCP(t *testing.T) {
	srv := newTestServer(t)

	saveResp := toolCall(t, srv, "mnemos_save", map[string]any{
		"title":      "Use WAL mode",
		"content":    "WAL mode allows concurrent readers in SQLite.",
		"type":       "pattern",
		"tags":       []string{"sqlite", "performance"},
		"importance": 8,
	})
	var saved map[string]any
	if err := json.Unmarshal([]byte(toolResultText(t, saveResp)), &saved); err != nil {
		t.Fatal(err)
	}
	id, _ := saved["id"].(string)
	if id == "" {
		t.Fatal("save returned no id")
	}

	searchResp := toolCall(t, srv, "mnemos_search", map[string]any{
		"query": "WAL",
	})
	if !strings.Contains(toolResultText(t, searchResp), "Use WAL mode") {
		t.Errorf("search did not return the saved observation")
	}

	getResp := toolCall(t, srv, "mnemos_get", map[string]any{"id": id})
	if !strings.Contains(toolResultText(t, getResp), "concurrent readers") {
		t.Errorf("get did not return full content")
	}
}

func TestSessionLifecycleViaMCP(t *testing.T) {
	srv := newTestServer(t)
	startResp := toolCall(t, srv, "mnemos_session_start", map[string]any{
		"project": "mnemos",
		"goal":    "wire MCP",
	})
	var started map[string]any
	_ = json.Unmarshal([]byte(toolResultText(t, startResp)), &started)
	sessID := started["session_id"].(string)

	endResp := toolCall(t, srv, "mnemos_session_end", map[string]any{
		"session_id": sessID,
		"summary":    "MCP wired",
		"reflection": "ndjson per line is the trick",
	})
	if !strings.Contains(toolResultText(t, endResp), "closed") {
		t.Errorf("session end did not confirm close")
	}
}

func TestSkillSaveAndMatchViaMCP(t *testing.T) {
	srv := newTestServer(t)
	toolCall(t, srv, "mnemos_skill_save", map[string]any{
		"name":        "wire-mcp-tool",
		"description": "Add a new MCP tool to the Mnemos server.",
		"procedure":   "1. Add definition in tools.go\n2. Register in registerTools\n3. Test via rpcCall",
		"tags":        []string{"mcp"},
	})
	matchResp := toolCall(t, srv, "mnemos_skill_match", map[string]any{
		"query": "add mcp tool",
	})
	if !strings.Contains(toolResultText(t, matchResp), "wire-mcp-tool") {
		t.Errorf("skill_match did not find the saved skill")
	}
}

func TestContextToolRespectsBudget(t *testing.T) {
	srv := newTestServer(t)
	for i := 0; i < 20; i++ {
		toolCall(t, srv, "mnemos_save", map[string]any{
			"title":   "entry",
			"content": "lots of sqlite FTS5 content about indexing and search ranking",
			"type":    "pattern",
		})
	}
	resp := toolCall(t, srv, "mnemos_context", map[string]any{
		"query":      "sqlite",
		"max_tokens": 200,
	})
	text := toolResultText(t, resp)
	if !strings.Contains(text, "token_estimate") {
		t.Errorf("context result missing token_estimate")
	}
}

func TestUnknownMethodReturnsError(t *testing.T) {
	srv := newTestServer(t)
	resp := rpcCall(t, srv, 1, "does/not/exist", nil)
	if resp["error"] == nil {
		t.Errorf("expected error for unknown method")
	}
}

func TestNotificationDoesNotRespond(t *testing.T) {
	srv := newTestServer(t)
	// notifications/initialized is a notification (no ID).
	req := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), bytes.NewReader(req), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("notification should not produce a response, got: %s", out.String())
	}
}

func TestResourceRead(t *testing.T) {
	srv := newTestServer(t)
	resp := rpcCall(t, srv, 1, "resources/list", nil)
	result := resp["result"].(map[string]any)
	resources := result["resources"].([]any)
	if len(resources) != 3 {
		t.Errorf("expected 3 resources, got %d", len(resources))
	}
	readResp := rpcCall(t, srv, 2, "resources/read", map[string]any{
		"uri": "mnemos://stats",
	})
	if readResp["error"] != nil {
		t.Errorf("stats read failed: %v", readResp["error"])
	}
}
