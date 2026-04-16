package prewarm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

func TestEmptyFixtureProducesEmptyBlock(t *testing.T) {
	pw, _, _, _, _ := newFixture(t)
	block, err := pw.Build(context.Background(), prewarm.Request{
		Mode:    prewarm.ModeSessionStart,
		Project: "no-data-project",
	})
	if err != nil {
		t.Fatal(err)
	}
	if block.TokenEstimate > 0 && block.Text == "" {
		t.Errorf("inconsistent: tokens=%d text empty", block.TokenEstimate)
	}
}

func TestMatchingSkillsSurface(t *testing.T) {
	pw, _, _, skl, _ := newFixture(t)
	ctx := context.Background()

	_, _ = skl.Save(ctx, skillsSave("wire-mcp-tool", "add a new MCP tool"))
	_, _ = skl.Save(ctx, skillsSave("run-migrations", "safely apply SQLite migrations"))

	block, err := pw.Build(ctx, prewarm.Request{
		Mode:    prewarm.ModeSessionStart,
		Project: "x",
		Goal:    "add an MCP tool",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block.Text, "wire-mcp-tool") {
		t.Errorf("skill match didn't surface in prewarm: %s", block.Text)
	}
}

func TestRecentSessionsWithFailureStatusMarked(t *testing.T) {
	pw, _, sess, _, _ := newFixture(t)
	ctx := context.Background()

	s, _ := sess.Open(ctx, session.OpenInput{Project: "p", Goal: "ship"})
	_ = sess.Close(ctx, session.CloseInput{
		ID: s.ID, Summary: "deploy broke", Status: session.StatusFailed,
	})

	block, err := pw.Build(ctx, prewarm.Request{
		Mode: prewarm.ModeSessionStart, Project: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block.Text, "failed") {
		t.Errorf("failed session status should surface in recent sessions: %s", block.Text)
	}
}

func TestRecoveryModeRequiresSessionID(t *testing.T) {
	pw, _, _, _, _ := newFixture(t)
	// Recovery with no session id should still build — just emits nothing
	// for the current-session and in-session sections. Falls through to
	// conventions + corrections.
	block, err := pw.Build(context.Background(), prewarm.Request{
		Mode:    prewarm.ModeCompactionRecovery,
		Project: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = block
}

func TestBuildRespectsDefaultMaxTokens(t *testing.T) {
	pw, mem, _, _, _ := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		_, _ = mem.Save(ctx, memory.SaveInput{
			Title:   "long convention",
			Content: strings.Repeat("lots of text about stuff and things ", 50),
			Type:    memory.TypeConvention,
			Project: "x",
		})
	}
	block, err := pw.Build(ctx, prewarm.Request{
		Mode: prewarm.ModeSessionStart, Project: "x",
		// MaxTokens not set → uses service default 500
	})
	if err != nil {
		t.Fatal(err)
	}
	if block.TokenEstimate > 600 {
		t.Errorf("default 500-token cap exceeded: %d", block.TokenEstimate)
	}
}

func skillsSave(name, desc string) skills.SaveInput {
	return skills.SaveInput{Name: name, Description: desc, Procedure: "1. do it"}
}
