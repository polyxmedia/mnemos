package prewarm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
)

func TestHotFilesAppearInSessionStartBlock(t *testing.T) {
	pw, mem, _, _, db := newFixture(t)
	ctx := context.Background()

	_, _ = mem.Save(ctx, memory.SaveInput{
		Title: "seed", Content: "anything", Type: memory.TypePattern,
		Project: "mnemos",
	})

	touches := db.Touches()
	for _, p := range []string{"handler.go", "handler.go", "handler.go", "util.go"} {
		if err := touches.Record(ctx, memory.TouchInput{
			Project: "mnemos", AgentID: "default", Path: p,
		}); err != nil {
			t.Fatal(err)
		}
	}

	block, err := pw.Build(ctx, prewarm.Request{
		Mode:    prewarm.ModeSessionStart,
		Project: "mnemos",
		Goal:    "fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block.Text, "handler.go") {
		t.Errorf("hot files section missing handler.go: %q", block.Text)
	}
	if !strings.Contains(block.Text, "hot files") {
		t.Errorf("expected 'hot files' heading in block")
	}
}
