package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// saveCued saves a memory and attaches cues to it, returning its id.
func saveCued(t *testing.T, title, content, project string, cues ...string) string {
	t.Helper()
	ctx := context.Background()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	res, err := d.mem.Save(ctx, memory.SaveInput{
		Title: title, Content: content, Type: memory.TypeConvention, Project: project,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.db.Cues().Add(ctx, res.Observation.ID, cues, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return res.Observation.ID
}

func runPreTool(t *testing.T, payload map[string]any) string {
	t.Helper()
	var out string
	withStdin(t, hookPayload(t, payload), func() {
		out = captureStdout(t, func() {
			if err := runHookPreTool(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
		})
	})
	return out
}

// preToolContext decodes the additionalContext of a PreToolUse reply and
// fails if the reply would decide the call.
func preToolContext(t *testing.T, out string) string {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return ""
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("expected JSON, got %q: %v", out, err)
	}
	hso, _ := decoded["hookSpecificOutput"].(map[string]any)
	if _, decides := hso["permissionDecision"]; decides {
		t.Fatal("a cue must inform the call, never decide it")
	}
	text, _ := hso["additionalContext"].(string)
	return text
}

func eventsOn(t *testing.T, channel string) []storage.RetrievalEvent {
	t.Helper()
	ctx := context.Background()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	all, err := d.db.RetrievalEvents().Since(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var out []storage.RetrievalEvent
	for _, e := range all {
		if e.Channel == channel {
			out = append(out, e)
		}
	}
	return out
}

func bash(cwd, host, command string) map[string]any {
	return map[string]any{
		"hook_event_name": "PreToolUse", "session_id": host, "cwd": cwd,
		"tool_name": "Bash", "tool_input": map[string]any{"command": command},
	}
}

func TestCommandCueFiresOnceInItsWindow(t *testing.T) {
	withHome(t)
	cwd := filepath.Join(t.TempDir(), "proj")
	sess := openHostSession(t, "proj", "host-1", "")
	id := saveCued(t, "commit messages carry no attribution", "commits read as human-authored", "proj", "cmd:git commit")

	got := preToolContext(t, runPreTool(t, bash(cwd, "host-1", "git add -A && git commit -m wip")))
	if !strings.Contains(got, "commit messages carry no attribution") || !strings.Contains(got, "cue cmd:git commit") {
		t.Fatalf("the cue should fire on git commit, got %q", got)
	}

	ctx := context.Background()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inj, err := d.db.Injections().ListByRef(ctx, injection.KindObservation, id, 10)
	d.close()
	if err != nil {
		t.Fatal(err)
	}
	if len(inj) != 1 || inj[0].Channel != injection.ChannelCue || inj[0].SessionID != sess.ID {
		t.Fatalf("want one cue injection on the open session, got %+v", inj)
	}
	events := eventsOn(t, storage.EventCue)
	if len(events) != 1 {
		t.Fatalf("want one cue event, got %d", len(events))
	}
	e := events[0]
	if !e.Fired || len(e.Injected) != 1 || e.Injected[0] != id || e.Chars == 0 || e.HostSessionID != "host-1" {
		t.Errorf("cue event = %+v", e)
	}

	// Ten commits in a stretch get the rule once.
	if again := preToolContext(t, runPreTool(t, bash(cwd, "host-1", "git commit --amend"))); again != "" {
		t.Errorf("a cue that just fired must stay quiet in its window, got %q", again)
	}
}

func TestCommandCueRespectsWordsAndScope(t *testing.T) {
	withHome(t)
	cwd := filepath.Join(t.TempDir(), "proj")
	saveCued(t, "proj only rule", "applies to proj", "proj", "cmd:git commit")
	saveCued(t, "other repo rule", "applies elsewhere", "other", "cmd:git commit")
	saveCued(t, "global heroku rule", "check the app name first", "", "cmd:heroku")

	for _, command := range []string{"git status", "echo git commit", "git log --grep commit"} {
		if got := preToolContext(t, runPreTool(t, bash(cwd, "h", command))); got != "" {
			t.Errorf("%q must not fire, got %q", command, got)
		}
	}
	got := preToolContext(t, runPreTool(t, bash(cwd, "h", "git commit -m x; heroku ps -a site")))
	if !strings.Contains(got, "proj only rule") || !strings.Contains(got, "global heroku rule") {
		t.Errorf("project and global cues should both fire, got %q", got)
	}
	if strings.Contains(got, "other repo rule") {
		t.Errorf("another project's cue fired: %q", got)
	}
}

func TestPathCueFiresBeforeEdit(t *testing.T) {
	withHome(t)
	cwd := filepath.Join(t.TempDir(), "proj")
	id := saveCued(t, "migrations are locked once released", "add a new migration, never edit one", "proj", "path:**/migrations/*.sql")

	edit := func(path string) string {
		return preToolContext(t, runPreTool(t, map[string]any{
			"hook_event_name": "PreToolUse", "session_id": "h", "cwd": cwd,
			"tool_name": "Edit", "tool_input": map[string]any{"file_path": path, "old_string": "a", "new_string": "b"},
		}))
	}
	if got := edit("internal/storage/queries.sql"); strings.Contains(got, "migrations are locked") {
		t.Errorf("a sql file outside migrations must not fire the cue: %q", got)
	}
	got := edit(filepath.Join(cwd, "internal", "storage", "migrations", "0003_x.sql"))
	if !strings.Contains(got, "migrations are locked") || !strings.Contains(got, "editing 0003_x.sql") {
		t.Fatalf("path cue should fire on a migration edit, got %q", got)
	}
	events := eventsOn(t, storage.EventCue)
	if len(events) != 1 || events[0].Injected[0] != id || events[0].Query != "internal/storage/migrations/0003_x.sql" {
		t.Errorf("cue event should carry the repo-relative path: %+v", events)
	}
}

func TestPromptEventsLogBothArmsAndIndexStaysSilent(t *testing.T) {
	withHome(t)
	writeConfig(t, "[retrieval]\nmode = \"experiment\"\n")
	saveMemory(t, "kubernetes rollout canary decision", "proj")
	cwd := filepath.Join(t.TempDir(), "proj")
	const prompt = "kubernetes rollout canary decision needs revisiting"
	indexHost, pushHost := hostOnArm(t, session.ArmIndex), hostOnArm(t, session.ArmPush)

	runPrewarmWith(t, map[string]any{"session_id": indexHost, "cwd": cwd})
	runPrewarmWith(t, map[string]any{"session_id": pushHost, "cwd": cwd})
	runUserPrompt(t, map[string]any{"session_id": indexHost, "cwd": cwd, "prompt": prompt})
	runUserPrompt(t, map[string]any{"session_id": pushHost, "cwd": cwd, "prompt": prompt})

	byHost := map[string]storage.RetrievalEvent{}
	for _, e := range eventsOn(t, storage.EventPromptHook) {
		byHost[e.HostSessionID] = e
	}
	idx, push := byHost[indexHost], byHost[pushHost]
	if idx.Arm != string(session.ArmIndex) || push.Arm != string(session.ArmPush) {
		t.Fatalf("events should carry their arm: index=%q push=%q", idx.Arm, push.Arm)
	}
	if idx.Fired || len(idx.Injected) != 0 || idx.Chars != 0 {
		t.Errorf("index arm must log nothing shown: %+v", idx)
	}
	if len(idx.Candidates) == 0 || idx.TopScore == 0 {
		t.Errorf("index arm must still log what push would have shown: %+v", idx)
	}
	if !push.Fired || len(push.Injected) == 0 || push.Chars == 0 {
		t.Errorf("push arm should log what it showed: %+v", push)
	}
	if push.Query != prompt {
		t.Errorf("event should keep the prompt for labelling, got %q", push.Query)
	}

	// A start block can be empty on the push arm; the index arm always
	// carries its titles list.
	starts := map[string]storage.RetrievalEvent{}
	for _, e := range eventsOn(t, storage.EventPrewarm) {
		starts[e.HostSessionID] = e
	}
	if len(starts) != 2 {
		t.Fatalf("want a prewarm event per conversation, got %v", starts)
	}
	if e := starts[indexHost]; e.Chars == 0 || !e.Fired || e.Arm != string(session.ArmIndex) {
		t.Errorf("index arm prewarm event = %+v", e)
	}
	if e := starts[pushHost]; e.Fired != (e.Chars > 0) {
		t.Errorf("push arm prewarm event: fired must follow chars: %+v", e)
	}
}

func TestScriptedRunsInClaudeTempStayOutOfTheExperiment(t *testing.T) {
	for dir, want := range map[string]bool{
		"/private/tmp/claude-501/-Users-x-Code-mnemos/abc/scratchpad/e2e": true,
		"/tmp/claude-1000/proj":            true,
		"/Users/x/Code/mnemos":             false,
		"/var/folders/qz/T/TestX/001/proj": false,
		"/Users/x/tmp/claudette":           false,
		"":                                 false,
	} {
		if got := isClaudeTempWorkDir(dir); got != want {
			t.Errorf("isClaudeTempWorkDir(%q) = %v, want %v", dir, got, want)
		}
	}
	host := hostOnArm(t, session.ArmIndex)
	if arm := assignArm(session.ModeExperiment, host, "/private/tmp/claude-501/x/scratchpad"); arm != "" {
		t.Errorf("a scripted run got arm %q", arm)
	}
	if arm := assignArm(session.ModeExperiment, host, "/Users/x/Code/mnemos"); arm != session.ArmIndex {
		t.Errorf("a real run lost its arm: %q", arm)
	}
}
