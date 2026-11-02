package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/experiment"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
)

// writeConfig gives the temp home from withHome a config file.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".mnemos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hostOnArm finds a host session ID experiment mode assigns to arm.
func hostOnArm(t *testing.T, arm session.RetrievalArm) string {
	t.Helper()
	for i := 0; i < 64; i++ {
		host := fmt.Sprintf("host-%d", i)
		if session.AssignArm(session.ModeExperiment, host) == arm {
			return host
		}
	}
	t.Fatalf("no host lands on %q", arm)
	return ""
}

func saveMemory(t *testing.T, title, project string) {
	t.Helper()
	saveMemoryOfType(t, title, project, memory.TypeDecision)
}

func runPrewarmWith(t *testing.T, payload map[string]any, args ...string) string {
	t.Helper()
	var out string
	withStdin(t, hookPayload(t, payload), func() {
		out = captureStdout(t, func() {
			if err := runPrewarm(context.Background(), args); err != nil {
				t.Fatal(err)
			}
		})
	})
	return out
}

func runUserPrompt(t *testing.T, payload map[string]any) string {
	t.Helper()
	var out string
	withStdin(t, hookPayload(t, payload), func() {
		out = captureStdout(t, func() {
			if err := runHookUserPrompt(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
		})
	})
	return out
}

func TestExperimentArmsGetDifferentRetrieval(t *testing.T) {
	withHome(t)
	writeConfig(t, "[retrieval]\nmode = \"experiment\"\n")
	saveMemory(t, "kubernetes rollout canary decision", "proj")
	cwd := filepath.Join(t.TempDir(), "proj")

	const prompt = "kubernetes rollout canary decision needs revisiting"
	indexHost, pushHost := hostOnArm(t, session.ArmIndex), hostOnArm(t, session.ArmPush)

	indexStart := runPrewarmWith(t, map[string]any{"session_id": indexHost, "cwd": cwd})
	if !strings.Contains(indexStart, "## memory index") || !strings.Contains(indexStart, "kubernetes rollout") {
		t.Errorf("index arm should start with the index:\n%s", indexStart)
	}
	if out := runUserPrompt(t, map[string]any{"session_id": indexHost, "cwd": cwd, "prompt": prompt}); out != "" {
		t.Errorf("index arm's prompt hook should inject nothing:\n%s", out)
	}

	pushStart := runPrewarmWith(t, map[string]any{"session_id": pushHost, "cwd": cwd})
	if strings.Contains(pushStart, "## memory index") {
		t.Errorf("push arm should not get the index:\n%s", pushStart)
	}
	if out := runUserPrompt(t, map[string]any{"session_id": pushHost, "cwd": cwd, "prompt": prompt}); !strings.Contains(out, "kubernetes rollout") {
		t.Errorf("push arm's prompt hook should inject the memory:\n%q", out)
	}

	// The agent must not be told which arm it is on.
	for _, text := range []string{indexStart, pushStart} {
		if strings.Contains(text, "experiment") || strings.Contains(text, "retrieval_arm") {
			t.Errorf("block names the experiment:\n%s", text)
		}
	}
}

func TestIndexArmStillGetsTheCaptureDirective(t *testing.T) {
	withHome(t)
	writeConfig(t, "[retrieval]\nmode = \"index\"\n")
	cwd := filepath.Join(t.TempDir(), "proj")
	runPrewarmWith(t, map[string]any{"session_id": "h", "cwd": cwd})
	out := runUserPrompt(t, map[string]any{"session_id": "h", "cwd": cwd, "prompt": "going forward we use postgres"})
	if !strings.Contains(out, "capture required") {
		t.Errorf("capture writes memory and should stay on every arm:\n%q", out)
	}
}

func TestDefaultConfigKeepsPushAndAssignsNothing(t *testing.T) {
	withHome(t)
	saveMemory(t, "kubernetes rollout canary decision", "proj")
	cwd := filepath.Join(t.TempDir(), "proj")
	start := runPrewarmWith(t, map[string]any{"session_id": "h", "cwd": cwd})
	if strings.Contains(start, "## memory index") {
		t.Errorf("default config must not change the session start:\n%s", start)
	}
	ctx := context.Background()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	sess, err := d.sess.Resolve(ctx, "h", "proj")
	if err != nil {
		t.Fatal(err)
	}
	if sess.RetrievalArm != "" {
		t.Errorf("default config must not assign an arm: %q", sess.RetrievalArm)
	}
	if out := runUserPrompt(t, map[string]any{"session_id": "h", "cwd": cwd, "prompt": "kubernetes rollout canary decision needs revisiting"}); !strings.Contains(out, "kubernetes rollout") {
		t.Errorf("default config should keep the prompt hook:\n%q", out)
	}
}

func TestResumeKeepsItsArmAndRecoveryRepeatsTheIndex(t *testing.T) {
	withHome(t)
	writeConfig(t, "[retrieval]\nmode = \"experiment\"\n")
	saveMemory(t, "kubernetes rollout canary decision", "proj")
	cwd := filepath.Join(t.TempDir(), "proj")
	host := hostOnArm(t, session.ArmIndex)

	runPrewarmWith(t, map[string]any{"session_id": host, "cwd": cwd})

	out := runPrewarmWith(t, map[string]any{"session_id": host, "cwd": cwd}, "--mode", "compaction_recovery", "--format", "json")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	index, _ := parsed["memory_index"].(string)
	if !strings.Contains(index, "kubernetes rollout") {
		t.Errorf("recovery should repeat the index, since compaction lost it: %s", out)
	}

	// Closed and resumed: a new mnemos session, the same conversation and arm.
	ctx := context.Background()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.sess.CloseForHost(ctx, host, "proj", session.CloseInput{Status: session.StatusOK}); err != nil {
		t.Fatal(err)
	}
	d.close()
	resumed := runPrewarmWith(t, map[string]any{"session_id": host, "cwd": cwd, "source": "resume"})
	if !strings.Contains(resumed, "## memory index") {
		t.Errorf("a resumed conversation must stay on its arm:\n%s", resumed)
	}
}

func TestPreToolSkipsAgentNoteFiles(t *testing.T) {
	withHome(t)
	saveMemoryOfType(t, "feedback notes convention", "proj", memory.TypeConvention)
	cwd := filepath.Join(t.TempDir(), "proj")
	var buf bytes.Buffer
	emitPreToolMemory(context.Background(), &buf, hookInput{
		CWD: cwd, ToolName: "Write",
		ToolInput: map[string]any{"file_path": "/h/.claude/projects/-x/memory/feedback_notes.md"},
	})
	if buf.Len() != 0 {
		t.Errorf("edits to the agent's notes should not pull memory: %s", buf.String())
	}
	emitPreToolMemory(context.Background(), &buf, hookInput{
		CWD: cwd, ToolName: "Write",
		ToolInput: map[string]any{"file_path": filepath.Join(cwd, "feedback_notes.go")},
	})
	if !strings.Contains(buf.String(), "feedback notes convention") {
		t.Errorf("the same name in the repo should still pull memory: %q", buf.String())
	}
}

// saveMemoryOfType stores a memory alongside unrelated filler, because BM25
// needs corpus contrast for a hit to clear the prompt hook's score floor.
func saveMemoryOfType(t *testing.T, title, project string, obsType memory.ObsType) {
	t.Helper()
	ctx := context.Background()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	for i, filler := range []string{"react hooks", "docker compose", "css grid", "git rebase", "tls certs"} {
		if _, err := d.mem.Save(ctx, memory.SaveInput{
			Title: filler, Content: "unrelated filler content " + strings.Repeat("noise ", i+1),
			Type: memory.TypeContext, Project: project,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.mem.Save(ctx, memory.SaveInput{
		Title: title, Content: "the " + title + " reasoning", Type: obsType, Project: project, Importance: 9,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHooksFileASubfolderSessionUnderItsRepo(t *testing.T) {
	withHome(t)
	repo := filepath.Join(t.TempDir(), "repo")
	sub := filepath.Join(repo, "cmd", "tool")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := projectFromHook(hookInput{CWD: sub}); got != "repo" {
		t.Errorf("project = %q, want the repo", got)
	}
	runPrewarmWith(t, map[string]any{"session_id": "h", "cwd": sub})
	ctx := context.Background()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	if _, err := d.sess.Resolve(ctx, "", "repo"); err != nil {
		t.Errorf("session should be filed under the repo: %v", err)
	}
}

func TestNativeCommandAndSearch(t *testing.T) {
	withHome(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)

	off := captureStdout(t, func() {
		if err := runNative(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(off, "index is off") {
		t.Errorf("should say how to enable it: %s", off)
	}

	writeConfig(t, "[native_memory]\nenabled = true\n")
	dir := filepath.Join(root, "projects", "-x-other-repo", "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	note := "---\nname: Career profile\ndescription: Roles held\ntype: user\n---\nBody"
	if err := os.WriteFile(filepath.Join(dir, "profile.md"), []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runNative(context.Background(), []string{"sync"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "1 notes across 1 repos") {
		t.Errorf("sync output: %s", out)
	}

	found := captureStdout(t, func() {
		if err := runSearch(context.Background(), []string{"career"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(found, "Career profile") {
		t.Errorf("mnemos search should include notes: %s", found)
	}

	if err := runNative(context.Background(), []string{"bogus"}); err == nil {
		t.Error("unknown subcommand should fail")
	}
}

func TestRunExperimentReadsTranscriptsByHostID(t *testing.T) {
	withHome(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	writeConfig(t, "[retrieval]\nmode = \"experiment\"\n")

	empty := captureStdout(t, func() {
		if err := runExperiment(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(empty, "no conversations assigned") {
		t.Errorf("empty report: %s", empty)
	}

	cwd := filepath.Join(t.TempDir(), "proj")
	host := hostOnArm(t, session.ArmIndex)
	runPrewarmWith(t, map[string]any{"session_id": host, "cwd": cwd})
	runPrewarmWith(t, map[string]any{"session_id": hostOnArm(t, session.ArmPush), "cwd": cwd})

	dir := filepath.Join(root, "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"user","message":{"content":"tried it again still not working"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__mnemos__mnemos_get","input":{"id":"X"}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, host+".jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runExperiment(context.Background(), []string{"--format", "json"}); err != nil {
			t.Fatal(err)
		}
	})
	var report experiment.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	if report.Index.Assigned != 1 || report.Index.Measured != 1 || report.Index.Totals.Opens != 1 || report.Index.Totals.Pushback != 1 {
		t.Errorf("index arm: %+v", report.Index)
	}
	if report.Push.Assigned != 1 || report.Push.Measured != 0 {
		t.Errorf("push arm has no transcript and should count as assigned only: %+v", report.Push)
	}

	text := captureStdout(t, func() {
		if err := runExperiment(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(text, "Too early to read") || !strings.Contains(text, "at random") {
		t.Errorf("text report: %s", text)
	}
}

func TestRunExperimentRejectsBadFlags(t *testing.T) {
	withHome(t)
	if err := runExperiment(context.Background(), []string{"--format", "xml"}); err == nil {
		t.Error("bad format should fail")
	}
	if err := runExperiment(context.Background(), []string{"--since", "-1h"}); err == nil {
		t.Error("negative window should fail")
	}
}
