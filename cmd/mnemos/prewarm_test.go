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
