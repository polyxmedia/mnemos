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
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// newRumHarness mirrors newHarness but additionally wires a rumination
// service so the mnemos_ruminate_* tools are registered. Returned alongside
// the skill service so tests can seed skills whose effectiveness trips the
// monitor.
type rumHarness struct {
	*harness
	skl *skills.Service
	rum *rumination.Service
}

func newRumHarness(t *testing.T) *rumHarness {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	skl := skills.NewService(skills.Config{Store: db.Skills()})
	rum := rumination.NewService(rumination.Config{
		Monitors: []rumination.Monitor{
			&rumination.SkillEffectivenessMonitor{Skills: skl},
		},
		Skills: skl,
		Memory: db.Observations(),
		Store:  db.Rumination(),
	})

	srv := mcp.NewServer(mcp.Config{
		Name:     "mnemos",
		Version:  "test",
		Memory:   memory.NewService(memory.Config{Store: db.Observations()}),
		Sessions: session.NewService(session.Config{Store: db.Sessions()}),
		Skills:   skl,
		Touches:  db.Touches(),
		Prewarm: prewarm.NewService(prewarm.Config{
			Observations: db.Observations(),
			Sessions:     db.Sessions(),
			Skills:       db.Skills(),
			Touches:      db.Touches(),
		}),
		Rumination: rum,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ctx := context.Background()
	t1, t2 := mcpsdk.NewInMemoryTransports()
	serverSession, err := srv.SDK().Connect(ctx, t1, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatal(err)
	}
	h := &harness{t: t, client: clientSession, server: serverSession}
	t.Cleanup(h.close)
	return &rumHarness{harness: h, skl: skl, rum: rum}
}

// seedFailingSkill saves a skill and records N unsuccessful uses so the
// effectiveness falls below the monitor's floor. Returns the saved ID.
func (h *rumHarness) seedFailingSkill(t *testing.T, name, procedure string, uses int) string {
	t.Helper()
	ctx := context.Background()
	saved, err := h.skl.Save(ctx, skills.SaveInput{
		Name: name, Description: "to ruminate", Procedure: procedure,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < uses; i++ {
		if err := h.skl.RecordUse(ctx, skills.FeedbackInput{ID: saved.ID, Success: false}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := h.rum.PersistDetected(ctx); err != nil {
		t.Fatal(err)
	}
	return saved.ID
}

func TestRuminate_ToolsExposedWhenServiceWired(t *testing.T) {
	h := newRumHarness(t)
	res, err := h.client.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"mnemos_ruminate_list":     true,
		"mnemos_ruminate_pack":     true,
		"mnemos_ruminate_resolve":  true,
		"mnemos_ruminate_dismiss":  true,
	}
	for _, tool := range res.Tools {
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Errorf("rumination tools missing: %v", want)
	}
}

func TestRuminate_ToolsHiddenWhenServiceNil(t *testing.T) {
	// The plain harness does NOT wire Rumination, so ruminate_* tools
	// must not appear. This is the "rumination.enabled = false" user path.
	h := newHarness(t)
	res, err := h.client.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if strings.HasPrefix(tool.Name, "mnemos_ruminate_") {
			t.Errorf("rumination tool %s leaked when service is nil", tool.Name)
		}
	}
}

func TestRuminate_ListPackResolveFlow(t *testing.T) {
	h := newRumHarness(t)
	h.seedFailingSkill(t, "retry on 401", "retry the request immediately", 12)

	// List: one pending candidate.
	_, listText := h.call("mnemos_ruminate_list", map[string]any{})
	var listed map[string]any
	if err := json.Unmarshal([]byte(listText), &listed); err != nil {
		t.Fatalf("unmarshal list: %v (raw: %s)", err, listText)
	}
	cands, ok := listed["candidates"].([]any)
	if !ok || len(cands) != 1 {
		t.Fatalf("want 1 candidate, got %v", listed["candidates"])
	}
	cand := cands[0].(map[string]any)
	candID, _ := cand["id"].(string)
	if candID == "" {
		t.Fatalf("candidate missing id: %+v", cand)
	}

	// Pack: should contain hypothesis + hostile review prompts.
	_, packText := h.call("mnemos_ruminate_pack", map[string]any{"id": candID})
	var packed map[string]any
	if err := json.Unmarshal([]byte(packText), &packed); err != nil {
		t.Fatalf("unmarshal pack: %v", err)
	}
	text, _ := packed["text"].(string)
	for _, want := range []string{
		"Rumination · retry on 401",
		"## Hypothesis under review",
		"## Hostile review",
		"ruminated-from:" + candID,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("pack text missing %q:\n%s", want, text)
		}
	}

	// Resolve: must require both resolved_by and a substantive why_better.
	res, _ := h.call("mnemos_ruminate_resolve", map[string]any{
		"id":          candID,
		"resolved_by": "sk-new-version",
		"why_better":  "", // empty → expect error
	})
	if !res.IsError {
		t.Errorf("empty why_better should error")
	}

	res, _ = h.call("mnemos_ruminate_resolve", map[string]any{
		"id":          candID,
		"resolved_by": "sk-new-version",
		"why_better":  "ok", // too short → expect error
	})
	if !res.IsError {
		t.Errorf("short why_better should error")
	}

	// Valid resolve.
	_, resolveText := h.call("mnemos_ruminate_resolve", map[string]any{
		"id":          candID,
		"resolved_by": "sk-new-version",
		"why_better":  "the revision predicts a token-refresh must precede retry on auth failures",
	})
	if !strings.Contains(resolveText, "resolved") {
		t.Errorf("resolve response should include status: %s", resolveText)
	}

	// List after resolve: zero pending.
	_, listAgain := h.call("mnemos_ruminate_list", map[string]any{})
	var after map[string]any
	_ = json.Unmarshal([]byte(listAgain), &after)
	cAfter, _ := after["candidates"].([]any)
	if len(cAfter) != 0 {
		t.Errorf("resolved candidate should not appear in pending list: %+v", cAfter)
	}
	counts, _ := after["counts"].(map[string]any)
	if counts["resolved"].(float64) != 1 {
		t.Errorf("resolved count mismatch: %+v", counts)
	}
}

func TestRuminate_DismissFlow(t *testing.T) {
	h := newRumHarness(t)
	h.seedFailingSkill(t, "weak skill", "do the thing", 12)

	_, listText := h.call("mnemos_ruminate_list", map[string]any{})
	var listed map[string]any
	_ = json.Unmarshal([]byte(listText), &listed)
	cands, _ := listed["candidates"].([]any)
	candID := cands[0].(map[string]any)["id"].(string)

	// Short reason rejected.
	res, _ := h.call("mnemos_ruminate_dismiss", map[string]any{
		"id":     candID,
		"reason": "eh",
	})
	if !res.IsError {
		t.Errorf("short dismiss reason should error")
	}

	// Valid dismiss.
	_, dismissText := h.call("mnemos_ruminate_dismiss", map[string]any{
		"id":     candID,
		"reason": "evidence was a one-off; the rule still holds in every other context",
	})
	if !strings.Contains(dismissText, "dismissed") {
		t.Errorf("dismiss response should include status: %s", dismissText)
	}
}

func TestRuminate_PackUnknownIDErrors(t *testing.T) {
	h := newRumHarness(t)
	res, _ := h.call("mnemos_ruminate_pack", map[string]any{"id": "rumination-does-not-exist"})
	if !res.IsError {
		t.Errorf("packing unknown id should error")
	}
}
