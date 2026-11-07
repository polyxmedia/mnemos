package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/experiment"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func TestExperimentStartCutsTheWindow(t *testing.T) {
	now := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		start string
		want  time.Time
	}{
		{"", now.Add(-30 * 24 * time.Hour)},
		{"2026-11-20", time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)},
		{"2026-11-20T15:04:05+01:00", time.Date(2026, 11, 20, 14, 4, 5, 0, time.UTC)},
		// Older than the look-back: the look-back wins.
		{"2025-01-01", now.Add(-30 * 24 * time.Hour)},
	}
	for _, c := range cases {
		got, err := experimentWindowStart(c.start, 30*24*time.Hour, now)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("start %q: got %v, %v; want %v", c.start, got, err, c.want)
		}
	}
	if _, err := experimentWindowStart("next tuesday", time.Hour, now); err == nil {
		t.Error("a bad experiment_start must fail the report")
	}
}

func TestBadExperimentStartLeavesHooksAlone(t *testing.T) {
	withHome(t)
	writeConfig(t, "[retrieval]\nmode = \"experiment\"\nexperiment_start = \"soon\"\n")
	saveMemory(t, "kubernetes rollout canary decision", "proj")
	cwd := filepath.Join(t.TempDir(), "proj")
	host := hostOnArm(t, session.ArmPush)
	runPrewarmWith(t, map[string]any{"session_id": host, "cwd": cwd})
	out := runUserPrompt(t, map[string]any{"session_id": host, "cwd": cwd, "prompt": "kubernetes rollout canary decision needs revisiting"})
	if !strings.Contains(out, "kubernetes rollout") {
		t.Errorf("the prompt hook must keep working with a bad experiment_start:\n%q", out)
	}
	if err := runExperiment(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "experiment_start") {
		t.Errorf("the report should name the bad key, got %v", err)
	}
}

func TestScriptedRunsLeaveNoEvents(t *testing.T) {
	withHome(t)
	saveMemory(t, "kubernetes rollout canary decision", "proj")
	cwd := "/private/tmp/claude-501/-Users-x-Code-proj/abc/scratchpad/proj"
	runPrewarmWith(t, map[string]any{"session_id": "scripted", "cwd": cwd})
	runUserPrompt(t, map[string]any{"session_id": "scripted", "cwd": cwd, "prompt": "kubernetes rollout canary decision"})
	for _, ch := range []string{storage.EventPrewarm, storage.EventPromptHook} {
		if got := eventsOn(t, ch); len(got) != 0 {
			t.Errorf("a scripted run logged %d %s events", len(got), ch)
		}
	}
}

func TestExperimentSampleThenScore(t *testing.T) {
	withHome(t)
	writeConfig(t, "[retrieval]\nmode = \"experiment\"\n")
	saveMemory(t, "kubernetes rollout canary decision", "proj")
	cwd := filepath.Join(t.TempDir(), "proj")
	for _, host := range []string{hostOnArm(t, session.ArmPush), hostOnArm(t, session.ArmIndex)} {
		runPrewarmWith(t, map[string]any{"session_id": host, "cwd": cwd})
		runUserPrompt(t, map[string]any{"session_id": host, "cwd": cwd, "prompt": "kubernetes rollout canary decision needs revisiting"})
	}

	file := filepath.Join(t.TempDir(), "sample.jsonl")
	var msg bytes.Buffer
	if err := runExperimentSample(context.Background(), &msg, []string{"--out", file}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.String(), "experiment score") {
		t.Errorf("sample should say what to do next: %s", msg.String())
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var labelled []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var p experiment.Pair
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("pair %q: %v", line, err)
		}
		rel := strings.Contains(p.Title, "kubernetes")
		p.Relevant = &rel
		out, _ := json.Marshal(p)
		labelled = append(labelled, string(out))
	}
	if len(labelled) < 2 {
		t.Fatalf("both prompts should be sampled, got %d pairs", len(labelled))
	}
	if err := os.WriteFile(file, []byte(strings.Join(labelled, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var report bytes.Buffer
	if err := runExperimentScore(context.Background(), &report, []string{file}); err != nil {
		t.Fatal(err)
	}
	out := report.String()
	for _, want := range []string{"labelled", "prompt_hook", "1/1 = 100%", "score floors"} {
		if !strings.Contains(out, want) {
			t.Errorf("score report missing %q:\n%s", want, out)
		}
	}
}
