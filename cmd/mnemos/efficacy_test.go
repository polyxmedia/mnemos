package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func TestRenderEfficacyEmpty(t *testing.T) {
	var b strings.Builder
	renderEfficacy(&b, &storage.EfficacyStats{}, 720*time.Hour)
	if !strings.Contains(b.String(), "nothing to attribute") {
		t.Errorf("no-session efficacy should say so, got: %q", b.String())
	}
}

func TestRenderEfficacyShape(t *testing.T) {
	var b strings.Builder
	renderEfficacy(&b, &storage.EfficacyStats{
		WithInjections:    storage.OutcomeSplit{Sessions: 10, OK: 9},
		WithoutInjections: storage.OutcomeSplit{Sessions: 20, OK: 6},
		ByChannel: []storage.ChannelEfficacy{
			{Channel: "prewarm", Surfacings: 100, Sessions: 8, OKSessions: 8},
		},
		ByMemory: []storage.MemoryEfficacy{
			{Kind: "observation", RefID: "x", Title: "wrap errors", Surfacings: 40, Sessions: 7, OKSessions: 6},
		},
	}, 720*time.Hour)
	out := b.String()
	for _, want := range []string{
		"last 720h",
		"with injections:     10 sessions, 9 ok (90%)",
		"without injections:  20 sessions, 6 ok (30%)",
		"gap: +60 percentage points",
		"prewarm",
		"[observation] wrap errors",
		"correlational, not causal",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("efficacy output missing %q:\n%s", want, out)
		}
	}
}

// TestRenderEfficacyMemoryFallsBackToRefID proves a surfaced memory with no
// resolvable title prints its ref id rather than a blank line.
func TestRenderEfficacyMemoryFallsBackToRefID(t *testing.T) {
	var b strings.Builder
	renderEfficacy(&b, &storage.EfficacyStats{
		WithInjections: storage.OutcomeSplit{Sessions: 1, OK: 1},
		ByMemory: []storage.MemoryEfficacy{
			{Kind: "observation", RefID: "01ABC", Title: "", Surfacings: 1, Sessions: 1, OKSessions: 1},
		},
	}, 24*time.Hour)
	if !strings.Contains(b.String(), "[observation] 01ABC") {
		t.Errorf("untitled memory should fall back to ref id, got:\n%s", b.String())
	}
}

func TestRunEfficacyEndToEnd(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	sess, err := d.sess.Open(ctx, session.OpenInput{Project: "mnemos", Goal: "test efficacy"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.sess.Close(ctx, session.CloseInput{ID: sess.ID, Summary: "done", Status: session.StatusOK}); err != nil {
		t.Fatal(err)
	}
	d.close()

	out := captureStdout(t, func() {
		if err := runEfficacy(ctx, []string{"--since", "1h"}); err != nil {
			t.Fatalf("efficacy: %v", err)
		}
	})
	// One ended session, no injections logged → the without-injections split.
	if !strings.Contains(out, "without injections:   1 sessions, 1 ok (100%)") {
		t.Errorf("expected the ended session in the without-injections split, got:\n%s", out)
	}

	jsonOut := captureStdout(t, func() {
		if err := runEfficacy(ctx, []string{"--since", "1h", "--format", "json"}); err != nil {
			t.Fatalf("efficacy json: %v", err)
		}
	})
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("json efficacy did not parse: %v\n%s", err, jsonOut)
	}
}

func TestRunEfficacyRejectsBadFlags(t *testing.T) {
	withHome(t)
	if err := runEfficacy(context.Background(), []string{"--format", "xml"}); err == nil {
		t.Error("invalid format must error")
	}
	if err := runEfficacy(context.Background(), []string{"--since", "-1h"}); err == nil {
		t.Error("negative window must error")
	}
}
