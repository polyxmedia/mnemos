package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/session"
)

// withStdin pipes s into os.Stdin for the duration of fn and restores the
// original on return. Matches the captureStdout helper pattern so hook
// commands (which read a JSON payload from stdin) can be driven in-process.
func withStdin(t *testing.T, s string, fn func()) {
	t.Helper()
	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = r
	go func() {
		_, _ = w.Write([]byte(s))
		_ = w.Close()
	}()
	defer func() { os.Stdin = orig }()
	fn()
}

func TestHookUserPromptBackfillsGoalOnEmptySession(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	sess, err := d.sess.Open(ctx, session.OpenInput{Project: "mnemos"})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	d.close()

	payload := `{"hook_event_name":"UserPromptSubmit","prompt":"add contradiction monitor to rumination"}`
	withStdin(t, payload, func() {
		if err := runHookUserPrompt(ctx, nil); err != nil {
			t.Fatalf("hook: %v", err)
		}
	})

	d2, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("reload deps: %v", err)
	}
	defer d2.close()
	got, err := d2.sess.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.Goal != "add contradiction monitor to rumination" {
		t.Errorf("expected goal backfilled, got %q", got.Goal)
	}
}

func TestHookUserPromptDoesNotOverrideExistingGoal(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	sess, err := d.sess.Open(ctx, session.OpenInput{Project: "mnemos", Goal: "ship thing"})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	d.close()

	payload := `{"hook_event_name":"UserPromptSubmit","prompt":"DIFFERENT prompt"}`
	withStdin(t, payload, func() {
		_ = runHookUserPrompt(ctx, nil)
	})

	d2, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("reload deps: %v", err)
	}
	defer d2.close()
	got, _ := d2.sess.Get(ctx, sess.ID)
	if got.Goal != "ship thing" {
		t.Errorf("existing goal must not be overridden, got %q", got.Goal)
	}
}

func TestHookUserPromptTruncatesLongPrompts(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	sess, err := d.sess.Open(ctx, session.OpenInput{Project: "mnemos"})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	d.close()

	long := strings.Repeat("x", 500)
	payload := `{"hook_event_name":"UserPromptSubmit","prompt":"` + long + `"}`
	withStdin(t, payload, func() {
		_ = runHookUserPrompt(ctx, nil)
	})

	d2, _ := loadDeps(ctx)
	defer d2.close()
	got, _ := d2.sess.Get(ctx, sess.ID)
	if len([]rune(got.Goal)) > maxAutoGoalChars {
		t.Errorf("goal must be truncated to %d chars, got %d", maxAutoGoalChars, len([]rune(got.Goal)))
	}
	if !strings.HasSuffix(got.Goal, "…") {
		t.Errorf("truncated goal should end with ellipsis, got %q", got.Goal)
	}
}

func TestHookUserPromptNoSessionIsSilent(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	payload := `{"hook_event_name":"UserPromptSubmit","prompt":"hi"}`
	withStdin(t, payload, func() {
		if err := runHookUserPrompt(ctx, nil); err != nil {
			t.Errorf("hook must not error when no session is open: %v", err)
		}
	})
}

func TestHookPostToolRecordsTouchForEdit(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	_, err = d.sess.Open(ctx, session.OpenInput{Project: "mnemos"})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	d.close()

	cwd := os.Getenv("HOME") + "/somewhere/mnemos"
	_ = os.MkdirAll(cwd, 0o755)
	payload := `{"hook_event_name":"PostToolUse","cwd":"` + cwd + `","tool_name":"Edit","tool_input":{"file_path":"internal/rumination/store.go","old_string":"x","new_string":"y"}}`
	withStdin(t, payload, func() {
		if err := runHookPostTool(ctx, nil); err != nil {
			t.Fatalf("hook: %v", err)
		}
	})

	d2, _ := loadDeps(ctx)
	defer d2.close()
	hot, err := d2.db.Touches().Hot(ctx, "", "mnemos", 10)
	if err != nil {
		t.Fatalf("hot: %v", err)
	}
	if len(hot) == 0 {
		t.Fatal("expected at least one touch recorded")
	}
	if hot[0].Path != "internal/rumination/store.go" {
		t.Errorf("expected touch for rumination/store.go, got %q", hot[0].Path)
	}
}

func TestHookPostToolIgnoresNonEditTools(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	payload := `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`
	withStdin(t, payload, func() {
		if err := runHookPostTool(ctx, nil); err != nil {
			t.Fatalf("hook: %v", err)
		}
	})

	d, _ := loadDeps(ctx)
	defer d.close()
	hot, _ := d.db.Touches().Hot(ctx, "", "", 10)
	if len(hot) != 0 {
		t.Errorf("non-edit tool must not record a touch, got %d", len(hot))
	}
}

func TestHookPostToolSilentWhenFilePathMissing(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	payload := `{"hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{}}`
	withStdin(t, payload, func() {
		if err := runHookPostTool(ctx, nil); err != nil {
			t.Errorf("hook must not error on missing file_path: %v", err)
		}
	})
}

func TestHookSessionEndClosesOpenSession(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	sess, err := d.sess.Open(ctx, session.OpenInput{Project: "mnemos", Goal: "ship hooks"})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	d.close()

	payload := `{"hook_event_name":"SessionEnd","reason":"logout"}`
	withStdin(t, payload, func() {
		if err := runHookSessionEnd(ctx, nil); err != nil {
			t.Fatalf("hook: %v", err)
		}
	})

	d2, _ := loadDeps(ctx)
	defer d2.close()
	got, err := d2.sess.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.EndedAt == nil {
		t.Fatal("expected session closed, ended_at still nil")
	}
	if got.Status != session.StatusOK {
		t.Errorf("expected StatusOK for reason=logout, got %v", got.Status)
	}
	foundTag := false
	for _, tag := range got.OutcomeTags {
		if tag == "auto-closed:logout" {
			foundTag = true
			break
		}
	}
	if !foundTag {
		t.Errorf("expected auto-closed:logout tag, got %v", got.OutcomeTags)
	}
}

func TestHookSessionEndMapsAbandonStatus(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	d, _ := loadDeps(ctx)
	sess, _ := d.sess.Open(ctx, session.OpenInput{Project: "mnemos"})
	d.close()

	withStdin(t, `{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`, func() {
		_ = runHookSessionEnd(ctx, nil)
	})

	d2, _ := loadDeps(ctx)
	defer d2.close()
	got, _ := d2.sess.Get(ctx, sess.ID)
	if got.Status != session.StatusAbandoned {
		t.Errorf("Ctrl+C exit must map to StatusAbandoned, got %v", got.Status)
	}
}

func TestHookSessionEndDoesNotReopenAlreadyClosed(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	d, _ := loadDeps(ctx)
	sess, _ := d.sess.Open(ctx, session.OpenInput{Project: "mnemos"})
	// Agent already closed it cleanly.
	_ = d.sess.Close(ctx, session.CloseInput{
		ID: sess.ID, Summary: "shipped", Status: session.StatusOK,
	})
	d.close()

	withStdin(t, `{"hook_event_name":"SessionEnd","reason":"logout"}`, func() {
		if err := runHookSessionEnd(ctx, nil); err != nil {
			t.Errorf("hook must not error on already-closed session: %v", err)
		}
	})

	d2, _ := loadDeps(ctx)
	defer d2.close()
	got, _ := d2.sess.Get(ctx, sess.ID)
	if got.Summary != "shipped" {
		t.Errorf("existing summary must be preserved, got %q", got.Summary)
	}
}

func TestHookSessionEndNoOpenSessionIsSilent(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	withStdin(t, `{"hook_event_name":"SessionEnd","reason":"logout"}`, func() {
		if err := runHookSessionEnd(ctx, nil); err != nil {
			t.Errorf("hook must not error when no session is open: %v", err)
		}
	})
}

func TestHookUserPromptEmptyPromptIsSilent(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	d, _ := loadDeps(ctx)
	sess, _ := d.sess.Open(ctx, session.OpenInput{Project: "mnemos"})
	d.close()

	withStdin(t, `{"hook_event_name":"UserPromptSubmit","prompt":""}`, func() {
		_ = runHookUserPrompt(ctx, nil)
	})

	d2, _ := loadDeps(ctx)
	defer d2.close()
	got, _ := d2.sess.Get(ctx, sess.ID)
	if got.Goal != "" {
		t.Errorf("empty prompt must not populate goal, got %q", got.Goal)
	}
}
