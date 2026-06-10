package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func TestFormatWindow(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{24 * time.Hour, "24h"},
		{168 * time.Hour, "168h"},
		{30 * time.Minute, "30m"},
		{90 * time.Second, "1m30s"},
	}
	for _, c := range cases {
		if got := formatWindow(c.in); got != c.want {
			t.Errorf("formatWindow(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderDigestEmpty(t *testing.T) {
	var b strings.Builder
	renderDigest(&b, &storage.DigestStats{}, 24*time.Hour)
	if !strings.Contains(b.String(), "no activity recorded") {
		t.Errorf("empty digest should say so, got: %q", b.String())
	}
}

func TestRenderDigestShape(t *testing.T) {
	var b strings.Builder
	renderDigest(&b, &storage.DigestStats{
		ObservationsSaved:  3,
		ObservationsByType: []storage.KindCount{{Kind: "correction", Count: 2}, {Kind: "decision", Count: 1}},
		SessionsOpened:     2,
		SessionsClosed:     1,
		SessionsByStatus:   []storage.KindCount{{Kind: "ok", Count: 1}},
		SkillsTouched:      1,
		InjectionsTotal:    7,
		InjectionsByChannel: []storage.KindCount{
			{Kind: "prewarm", Count: 5}, {Kind: "prompt_hook", Count: 2},
		},
		TopSurfaced: []storage.SurfacedCount{
			{Kind: "observation", RefID: "x", Title: "error wrapping", Count: 4},
		},
		DreamPasses: 1,
	}, 24*time.Hour)
	out := b.String()
	for _, want := range []string{
		"last 24h",
		"observations saved: 3 (correction: 2, decision: 1)",
		"sessions: 2 opened, 1 closed (ok: 1)",
		"skills created or updated: 1",
		"memories surfaced into context: 7 (prewarm: 5, prompt_hook: 2)",
		"[observation] error wrapping (4×)",
		"dream passes: 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("digest output missing %q:\n%s", want, out)
		}
	}
}

func TestRunDigestEndToEnd(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	sess, err := d.sess.Open(ctx, session.OpenInput{Project: "mnemos", Goal: "test digest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.mem.Save(ctx, memory.SaveInput{
		Title: "a correction", Content: "c", Type: memory.TypeCorrection,
		Project: "mnemos", SessionID: sess.ID,
	}); err != nil {
		t.Fatal(err)
	}
	d.close()

	out := captureStdout(t, func() {
		if err := runDigest(ctx, []string{"--since", "1h"}); err != nil {
			t.Fatalf("digest: %v", err)
		}
	})
	if !strings.Contains(out, "observations saved: 1") {
		t.Errorf("expected 1 observation in digest, got:\n%s", out)
	}
	if !strings.Contains(out, "1 opened") {
		t.Errorf("expected 1 session opened in digest, got:\n%s", out)
	}

	jsonOut := captureStdout(t, func() {
		if err := runDigest(ctx, []string{"--since", "1h", "--format", "json"}); err != nil {
			t.Fatalf("digest json: %v", err)
		}
	})
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("json digest did not parse: %v\n%s", err, jsonOut)
	}
}

func TestRunDigestRejectsBadFlags(t *testing.T) {
	withHome(t)
	if err := runDigest(context.Background(), []string{"--format", "xml"}); err == nil {
		t.Error("invalid format must error")
	}
	if err := runDigest(context.Background(), []string{"--since", "-1h"}); err == nil {
		t.Error("negative window must error")
	}
}
