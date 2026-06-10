package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/prewarm"
)

func TestParsePrewarmMode(t *testing.T) {
	cases := []struct {
		in      string
		want    prewarm.Mode
		wantErr bool
	}{
		{"session_start", prewarm.ModeSessionStart, false},
		{"", prewarm.ModeSessionStart, false},
		{"compaction_recovery", prewarm.ModeCompactionRecovery, false},
		{"garbage", 0, true},
	}
	for _, c := range cases {
		got, err := parsePrewarmMode(c.in)
		if c.wantErr && err == nil {
			t.Errorf("%q: expected error, got none", c.in)
			continue
		}
		if !c.wantErr && err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
	}
}

func TestReadHookStdinParsesClaudePayload(t *testing.T) {
	// A realistic Claude Code SessionStart payload.
	raw := `{
		"session_id": "abc123",
		"cwd": "/Users/demo/Code/mnemos",
		"hook_event_name": "SessionStart",
		"source": "startup"
	}`
	in := readHookStdin(strings.NewReader(raw))
	if in.SessionID != "abc123" {
		t.Errorf("session_id: got %q", in.SessionID)
	}
	if in.CWD != "/Users/demo/Code/mnemos" {
		t.Errorf("cwd: got %q", in.CWD)
	}
	if in.Source != "startup" {
		t.Errorf("source: got %q", in.Source)
	}
}

func TestReadHookStdinHandlesEmpty(t *testing.T) {
	in := readHookStdin(strings.NewReader(""))
	if in.CWD != "" || in.SessionID != "" {
		t.Errorf("expected zero value, got %+v", in)
	}
}

func TestReadHookStdinHandlesNonJSON(t *testing.T) {
	// A non-JSON stdin means the command was invoked interactively or
	// piped garbage. We must not panic or error — just return zero.
	in := readHookStdin(strings.NewReader("not json at all"))
	if in.CWD != "" {
		t.Errorf("non-json stdin should produce zero value, got %+v", in)
	}
}

func TestRunPrewarmTextOutput(t *testing.T) {
	home := homeWithConfig(t, "")
	_ = home

	out := captureStdout(t, func() {
		if err := runPrewarm(context.Background(), []string{"--project", "mnemos"}); err != nil {
			t.Fatalf("prewarm: %v", err)
		}
	})
	// A fresh DB has no observations, so the block text may be empty; but
	// the session_id must always be printed because open-session defaults
	// to true in session_start mode.
	if !strings.Contains(out, "mnemos_session_id:") {
		t.Errorf("expected session_id line in output, got: %q", out)
	}
}

func TestRunPrewarmJSONOutput(t *testing.T) {
	homeWithConfig(t, "")
	out := captureStdout(t, func() {
		if err := runPrewarm(context.Background(),
			[]string{"--project", "mnemos", "--format", "json"}); err != nil {
			t.Fatalf("prewarm: %v", err)
		}
	})
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("json output did not parse: %v\n%s", err, out)
	}
	if parsed["project"] != "mnemos" {
		t.Errorf("project missing or wrong: %v", parsed["project"])
	}
	if _, ok := parsed["session_id"].(string); !ok {
		t.Errorf("session_id missing: %v", parsed)
	}
	if _, ok := parsed["safety_risk"].(string); !ok {
		t.Errorf("safety_risk missing: %v", parsed)
	}
}

func TestRunPrewarmRejectsInvalidMode(t *testing.T) {
	homeWithConfig(t, "")
	err := runPrewarm(context.Background(), []string{"--mode", "wat"})
	if err == nil {
		t.Error("expected error on invalid mode")
	}
}

func TestRunPrewarmRejectsInvalidFormat(t *testing.T) {
	homeWithConfig(t, "")
	err := runPrewarm(context.Background(), []string{"--format", "xml"})
	if err == nil {
		t.Error("expected error on invalid format")
	}
}

func TestRunPrewarmNoSessionFlag(t *testing.T) {
	// With --open-session=false, the output should still render (empty
	// prewarm for a fresh DB is fine) but must not contain a session_id
	// line since we never opened one.
	homeWithConfig(t, "")
	out := captureStdout(t, func() {
		if err := runPrewarm(context.Background(),
			[]string{"--project", "mnemos", "--open-session=false"}); err != nil {
			t.Fatalf("prewarm: %v", err)
		}
	})
	if strings.Contains(out, "mnemos_session_id:") {
		t.Errorf("session_id should be absent when --open-session=false: %q", out)
	}
}

func TestRunPrewarmCompactionRecoveryMode(t *testing.T) {
	homeWithConfig(t, "")
	// Compaction recovery without any prior session is a no-op but must
	// not error. We also must NOT open a new session in this mode.
	out := captureStdout(t, func() {
		if err := runPrewarm(context.Background(),
			[]string{"--project", "mnemos", "--mode", "compaction_recovery"}); err != nil {
			t.Fatalf("prewarm: %v", err)
		}
	})
	if strings.Contains(out, "mnemos_session_id:") {
		t.Errorf("compaction_recovery should not open a new session when none exists: %q", out)
	}
}

func TestRunPrewarmAdoptsRecentOpenSession(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()

	// SessionStart hooks installed at user AND project scope both fire
	// within milliseconds. The second firing must adopt the session the
	// first one opened, not create a twin that never gets closed.
	first := captureStdout(t, func() {
		if err := runPrewarm(ctx, []string{"--project", "mnemos"}); err != nil {
			t.Fatalf("first prewarm: %v", err)
		}
	})
	second := captureStdout(t, func() {
		if err := runPrewarm(ctx, []string{"--project", "mnemos"}); err != nil {
			t.Fatalf("second prewarm: %v", err)
		}
	})

	firstID := sessionIDFromOutput(t, first)
	secondID := sessionIDFromOutput(t, second)
	if firstID != secondID {
		t.Errorf("second firing must adopt the open session: %s != %s", firstID, secondID)
	}

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	open, err := d.sess.ListOpen(ctx, "mnemos")
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 1 {
		t.Errorf("want exactly 1 open session after double firing, got %d", len(open))
	}
}

func TestRunPrewarmDoesNotAdoptAcrossProjects(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()

	captureStdout(t, func() {
		if err := runPrewarm(ctx, []string{"--project", "alpha"}); err != nil {
			t.Fatalf("prewarm alpha: %v", err)
		}
	})
	captureStdout(t, func() {
		if err := runPrewarm(ctx, []string{"--project", "beta"}); err != nil {
			t.Fatalf("prewarm beta: %v", err)
		}
	})

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	for _, proj := range []string{"alpha", "beta"} {
		open, err := d.sess.ListOpen(ctx, proj)
		if err != nil {
			t.Fatalf("list open %s: %v", proj, err)
		}
		if len(open) != 1 {
			t.Errorf("project %s: want its own session, got %d", proj, len(open))
		}
	}
}

// sessionIDFromOutput extracts the mnemos_session_id line from prewarm
// text output.
func sessionIDFromOutput(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "mnemos_session_id: ") {
			return strings.TrimPrefix(line, "mnemos_session_id: ")
		}
	}
	t.Fatalf("no mnemos_session_id in output: %q", out)
	return ""
}
