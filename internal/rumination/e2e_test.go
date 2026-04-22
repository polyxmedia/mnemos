package rumination_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/dream"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// TestRumination_EndToEnd is the integration test that exercises every
// layer: real SQLite store, all four monitors, dream detection, prewarm
// surface, resolve flow, dream auto-resolve, final counts. One test so
// the whole seam is covered in a single run — if any link breaks, this
// fails fast.
//
// Scenario:
//  1. Seed a skill that fails repeatedly → SkillEffectivenessMonitor fires.
//  2. Seed a convention and a correction that contradicts it →
//     ContradictionDetectedMonitor fires.
//  3. Seed a skill on a topic plus three post-creation corrections →
//     CorrectionRepeatUnderSkillMonitor fires.
//  4. Dream pass runs all monitors and persists candidates.
//  5. Prewarm surface shows the pending queue and inline badges on
//     matched skills.
//  6. Agent resolves one candidate via Service.Resolve, tags the skill
//     with ruminated-from:<id>.
//  7. Next dream pass auto-resolves a parallel tagged revision.
//  8. Final counts reflect the transitions.
func TestRumination_EndToEnd(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mem := memory.NewService(memory.Config{Store: db.Observations()})
	skl := skills.NewService(skills.Config{Store: db.Skills()})

	// --- 1. Failing skill (SkillEffectivenessMonitor target) ------------
	failing, err := skl.Save(ctx, skills.SaveInput{
		Name: "retry on 401", Description: "retry auth failures",
		Procedure: "retry the request immediately",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if err := skl.RecordUse(ctx, skills.FeedbackInput{ID: failing.ID, Success: false}); err != nil {
			t.Fatal(err)
		}
	}

	// --- 2. Convention + contradicting correction (ContradictionMonitor) --
	conv, err := mem.Save(ctx, memory.SaveInput{
		Title:   "use sqlite for storage",
		Content: "sqlite is sufficient at this scale",
		Type:    memory.TypeConvention,
		Project: "proj",
	})
	if err != nil {
		t.Fatal(err)
	}
	contra, err := mem.Save(ctx, memory.SaveInput{
		Title:   "postgres is the right call",
		Content: "after load testing we discovered sqlite locks at n concurrent writers",
		Type:    memory.TypeCorrection,
		Project: "proj",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Observations().Link(ctx,
		contra.Observation.ID, conv.Observation.ID, memory.LinkContradicts); err != nil {
		t.Fatal(err)
	}

	// --- 3. Skill + post-creation corrections (CorrectionRepeatMonitor) --
	oauthSkill, err := skl.Save(ctx, skills.SaveInput{
		Name: "oauth retry skill", Description: "auto-promoted from oauth corrections",
		Procedure: "retry on 401 after token refresh",
		Tags:      []string{"oauth"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Three corrections tagged "oauth" after the skill exists.
	for i := 0; i < 3; i++ {
		if _, err := mem.Save(ctx, memory.SaveInput{
			Title:   "oauth failed again " + string(rune('a'+i)),
			Content: "the skill procedure isn't covering this case",
			Type:    memory.TypeCorrection,
			Tags:    []string{"oauth"},
			Project: "proj",
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = oauthSkill // used indirectly via tag match

	// --- Wire all four monitors + dream pass ----------------------------
	rum := rumination.NewService(rumination.Config{
		Monitors: []rumination.Monitor{
			&rumination.SkillEffectivenessMonitor{Skills: skl},
			&rumination.StaleSkillMonitor{Skills: skl},
			&rumination.CorrectionRepeatUnderSkillMonitor{Corrections: db.Observations(), Skills: skl},
			&rumination.ContradictionDetectedMonitor{Links: db.Observations()},
		},
		Skills: skl,
		Memory: db.Observations(),
		Store:  db.Rumination(),
	})
	ds := dream.NewService(dream.Config{
		Memory: mem, Store: db.Observations(), Reader: db.Observations(),
		Skills: skl, Rumination: rum, Logger: log, StaleDays: 365,
	})

	// --- 4. First dream pass: all three conditions fire. ----------------
	j, err := ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	// Three conditions: failing skill, contradicted convention, repeat-under-skill.
	if j.RuminatedInserted < 3 {
		t.Fatalf("first pass should raise at least 3 candidates, got %d: %+v", j.RuminatedInserted, j)
	}
	pending, err := db.Rumination().Pending(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) < 3 {
		t.Fatalf("store should have at least 3 pending candidates, got %d", len(pending))
	}

	// Verify the three monitors all contributed.
	byMonitor := map[string]int{}
	for _, c := range pending {
		byMonitor[c.MonitorName]++
	}
	for _, name := range []string{
		"skill-effectiveness-floor",
		"correction-repeat-under-skill",
		"contradiction-detected",
	} {
		if byMonitor[name] == 0 {
			t.Errorf("monitor %q did not produce any candidates (byMonitor=%+v)", name, byMonitor)
		}
	}

	// --- 5. Prewarm surfaces the queue. ---------------------------------
	pw := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(), Sessions: db.Sessions(),
		Skills: db.Skills(), Touches: db.Touches(),
		Rumination: rum,
	})
	block, err := pw.Build(ctx, prewarm.Request{
		Mode: prewarm.ModeSessionStart, Project: "proj", Goal: "retry on 401",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block.Text, "## pending reviews") {
		t.Errorf("prewarm should include pending reviews section:\n%s", block.Text)
	}
	// Matched-skill inline badge: goal "retry on 401" matches the failing
	// skill, which has an open candidate → badge should be present.
	if !strings.Contains(block.Text, "[rumination pending:") {
		t.Errorf("prewarm should carry inline rumination badge on the matched skill:\n%s", block.Text)
	}

	// --- 6. Agent resolves the effectiveness candidate explicitly. ------
	var effCandID string
	for _, c := range pending {
		if c.MonitorName == "skill-effectiveness-floor" {
			effCandID = c.ID
			break
		}
	}
	if effCandID == "" {
		t.Fatal("no effectiveness candidate found to resolve")
	}
	// Agent writes a revised skill version with the provenance tag.
	revised, err := skl.Save(ctx, skills.SaveInput{
		Name: "retry on 401", Description: "retry auth failures after token refresh",
		Procedure: "refresh the access token first, then retry exactly once",
		Tags:      []string{"ruminated-from:" + effCandID},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Agent closes the candidate through the resolve tool (simulated directly).
	if err := rum.Resolve(ctx, effCandID, revised.ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// --- 7. Second dream pass: auto-resolve picks up the tagged revision
	// and closes any remaining tagged-but-unresolved candidates
	// (none in this scenario, but the path must not blow up and must
	// be idempotent against the one we already closed).
	j2, err := ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j2.RuminatedResolved != 0 {
		t.Errorf("no untouched tagged candidates should remain, got %d auto-resolved", j2.RuminatedResolved)
	}

	// --- 8. Final counts reflect the transitions. -----------------------
	counts, err := rum.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Resolved < 1 {
		t.Errorf("want at least 1 resolved, got %+v", counts)
	}
	// Pending should still include contradiction + repeat-under-skill.
	if counts.Pending < 2 {
		t.Errorf("want at least 2 pending after resolving one, got %+v", counts)
	}

	// --- Sanity: resolved candidate has resolved_by pointing at the
	// revised skill, and a Get returns the correct lifecycle state.
	got, err := db.Rumination().Get(ctx, effCandID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != rumination.StatusResolved {
		t.Errorf("resolved candidate status = %s", got.Status)
	}
	if got.ResolvedBy != revised.ID {
		t.Errorf("resolved_by = %s, want %s", got.ResolvedBy, revised.ID)
	}
}
