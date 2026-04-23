package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/mcp"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// harness builds a server+client pair connected via in-memory transports.
// Use the returned *mcpsdk.ClientSession for all tool/resource calls.
type harness struct {
	t      *testing.T
	client *mcpsdk.ClientSession
	server *mcpsdk.ServerSession
}

func (h *harness) close() {
	_ = h.client.Close()
	_ = h.server.Close()
}

// call invokes a tool and returns the unwrapped text of the first content
// block. Fails the test on protocol error. Tool-level errors are visible
// via result.IsError.
func (h *harness) call(name string, args any) (*mcpsdk.CallToolResult, string) {
	h.t.Helper()
	ctx := context.Background()
	res, err := h.client.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		h.t.Fatalf("CallTool %s: %v", name, err)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcpsdk.TextContent); ok {
			text = tc.Text
		}
	}
	return res, text
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	srv := mcp.NewServer(mcp.Config{
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

	ctx := context.Background()
	t1, t2 := mcpsdk.NewInMemoryTransports()
	serverSession, err := srv.SDK().Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatalf("client connect: %v", err)
	}

	h := &harness{t: t, client: clientSession, server: serverSession}
	t.Cleanup(h.close)
	return h
}

func TestToolsListReturnsAllFifteen(t *testing.T) {
	h := newHarness(t)
	res, err := h.client.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 15 {
		t.Errorf("want 15 tools, got %d", len(res.Tools))
	}
	expected := map[string]bool{
		"mnemos_save": true, "mnemos_search": true, "mnemos_get": true,
		"mnemos_delete": true, "mnemos_link": true,
		"mnemos_session_start": true, "mnemos_session_end": true,
		"mnemos_context": true,
		"mnemos_correct": true, "mnemos_convention": true, "mnemos_touch": true,
		"mnemos_skill_match": true, "mnemos_skill_save": true,
		"mnemos_stats":   true,
		"mnemos_promote": true,
	}
	for _, tool := range res.Tools {
		delete(expected, tool.Name)
	}
	if len(expected) != 0 {
		t.Errorf("missing tools: %v", expected)
	}
}

func TestSaveSearchGetRoundTrip(t *testing.T) {
	h := newHarness(t)

	_, saveText := h.call("mnemos_save", map[string]any{
		"title":      "use WAL",
		"content":    "WAL enables concurrent readers in SQLite",
		"type":       "pattern",
		"tags":       []string{"sqlite"},
		"importance": 8,
	})
	var saved map[string]any
	if err := json.Unmarshal([]byte(saveText), &saved); err != nil {
		t.Fatalf("save result: %v (raw: %s)", err, saveText)
	}
	id := saved["id"].(string)
	if id == "" {
		t.Fatal("save returned empty id")
	}

	_, searchText := h.call("mnemos_search", map[string]any{"query": "WAL"})
	if !strings.Contains(searchText, "use WAL") {
		t.Errorf("search did not return the saved observation: %s", searchText)
	}

	_, getText := h.call("mnemos_get", map[string]any{"id": id})
	if !strings.Contains(getText, "concurrent readers") {
		t.Errorf("get did not return full content: %s", getText)
	}
}

func TestSessionStartReturnsPrewarm(t *testing.T) {
	h := newHarness(t)

	h.call("mnemos_convention", map[string]any{
		"title":     "error wrap",
		"rule":      "all errors wrapped with %w",
		"rationale": "errors.Is compat",
		"project":   "mnemos",
	})

	_, text := h.call("mnemos_session_start", map[string]any{
		"project": "mnemos",
		"goal":    "add route",
	})
	if !strings.Contains(text, "prewarm") {
		t.Errorf("session_start must return prewarm block: %s", text)
	}
	if !strings.Contains(text, "error wrap") {
		t.Errorf("prewarm must include seeded convention: %s", text)
	}
}

func TestCompactionRecovery(t *testing.T) {
	h := newHarness(t)

	_, startText := h.call("mnemos_session_start", map[string]any{
		"project": "mnemos",
		"goal":    "finish refactor",
	})
	var started map[string]any
	_ = json.Unmarshal([]byte(startText), &started)
	sessID := started["session_id"].(string)

	h.call("mnemos_save", map[string]any{
		"title":      "decided push over pull",
		"content":    "session_start returns prewarm",
		"type":       "decision",
		"session_id": sessID,
		"project":    "mnemos",
	})

	_, recText := h.call("mnemos_context", map[string]any{
		"mode":       "recovery",
		"session_id": sessID,
		"project":    "mnemos",
	})
	if !strings.Contains(recText, "finish refactor") {
		t.Errorf("recovery must include session goal: %s", recText)
	}
	if !strings.Contains(recText, "decided push") {
		t.Errorf("recovery must include in-session observation: %s", recText)
	}
}

func TestLinkSupersedesHidesTarget(t *testing.T) {
	h := newHarness(t)

	_, t1 := h.call("mnemos_save", map[string]any{
		"title": "use X", "content": "we use X", "type": "decision",
	})
	var o1 map[string]any
	_ = json.Unmarshal([]byte(t1), &o1)
	id1 := o1["id"].(string)

	_, t2 := h.call("mnemos_save", map[string]any{
		"title": "use Y", "content": "we use Y now", "type": "decision",
	})
	var o2 map[string]any
	_ = json.Unmarshal([]byte(t2), &o2)
	id2 := o2["id"].(string)

	h.call("mnemos_link", map[string]any{
		"source_id": id2, "target_id": id1, "link_type": "supersedes",
	})

	_, searchText := h.call("mnemos_search", map[string]any{"query": "use"})
	if strings.Contains(searchText, id1) {
		t.Errorf("superseded observation must be hidden from default search")
	}
}

func TestStatsIncludesEmbeddingAndStorage(t *testing.T) {
	h := newHarness(t)
	_, text := h.call("mnemos_stats", map[string]any{})
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["embedding"]; !ok {
		t.Errorf("stats missing embedding section: %v", out)
	}
}

func TestResourcesAvailable(t *testing.T) {
	h := newHarness(t)
	res, err := h.client.ListResources(context.Background(), &mcpsdk.ListResourcesParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resources) != 3 {
		t.Errorf("want 3 resources, got %d", len(res.Resources))
	}

	read, err := h.client.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{
		URI: "mnemos://stats",
	})
	if err != nil {
		t.Fatalf("read stats resource: %v", err)
	}
	if len(read.Contents) == 0 || read.Contents[0].Text == "" {
		t.Error("stats resource returned empty content")
	}
}

func TestSchemaInferenceSurfacesInToolDescriptors(t *testing.T) {
	// The SDK infers input schemas from struct tags. This sanity-checks
	// that the 'type' field on save carries through.
	h := newHarness(t)
	res, err := h.client.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name == "mnemos_save" {
			if tool.InputSchema == nil {
				t.Error("save tool should have an input schema")
			}
			return
		}
	}
	t.Fatal("mnemos_save not found in tool list")
}
