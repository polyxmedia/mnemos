package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
)

// runHook is the parent dispatcher for harness-side hook subcommands. Each
// leaf command reads a Claude Code hook payload from stdin (shape documented
// at https://code.claude.com/docs/en/hooks) and performs one invisible,
// best-effort side effect against the mnemos store. Failure is always silent
// at exit 0 — hooks must never block the user or spam the transcript.
func runHook(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mnemos hook <user-prompt|stop|post-tool>")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "user-prompt":
		return runHookUserPrompt(ctx, rest)
	case "post-tool":
		return runHookPostTool(ctx, rest)
	case "session-end":
		return runHookSessionEnd(ctx, rest)
	default:
		return fmt.Errorf("unknown hook: %s", sub)
	}
}

// maxAutoGoalChars caps the goal we backfill from a user prompt. Goals are
// shown in prewarm output and session lists, so we keep them one-line.
const maxAutoGoalChars = 120

// runHookUserPrompt handles Claude Code's UserPromptSubmit event. On every
// user prompt it checks whether the current open session for this cwd has
// a goal; if not, it backfills the first ~120 characters of the prompt so
// the session stops being an anonymous timestamp. Idempotent — subsequent
// prompts are no-ops because SetGoalIfEmpty guards on the goal column.
func runHookUserPrompt(ctx context.Context, _ []string) error {
	in := readHookStdin(os.Stdin)
	if in.Prompt == "" {
		return nil
	}

	d, err := loadDeps(ctx)
	if err != nil {
		// Degraded silent: hooks must not fail loudly. Write nothing to
		// stdout (Claude injects stdout into context for UserPromptSubmit).
		fmt.Fprintln(os.Stderr, "mnemos hook user-prompt:", err)
		return nil
	}
	defer d.close()

	sess, err := d.sess.Current(ctx, "")
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil
		}
		fmt.Fprintln(os.Stderr, "mnemos hook user-prompt:", err)
		return nil
	}
	if sess == nil || sess.Goal != "" {
		return nil
	}

	if err := d.sess.SetGoalIfEmpty(ctx, sess.ID, truncateGoal(in.Prompt)); err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook user-prompt:", err)
	}
	return nil
}

// truncateGoal folds whitespace and caps to maxAutoGoalChars, appending an
// ellipsis when truncated. Pure helper; no I/O.
func truncateGoal(s string) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if len(s) <= maxAutoGoalChars {
		return s
	}
	return s[:maxAutoGoalChars-1] + "…"
}

// runHookPostTool handles Claude Code's PostToolUse event. For file-editing
// tools (Edit / Write / MultiEdit / NotebookEdit), it records a passive
// touch against the heat map so the store stays populated even when the
// agent never calls mnemos_touch. Matcher filtering happens at the Claude
// Code level; this command is defensive and no-ops on any other tool.
//
// The matcher we install (`Edit|Write|MultiEdit|NotebookEdit`) is an exact
// alternation — the schema documents this as the format when the string has
// no regex metacharacters but contains pipes.
func runHookPostTool(ctx context.Context, _ []string) error {
	in := readHookStdin(os.Stdin)
	if !isFileEditTool(in.ToolName) {
		return nil
	}
	path := filePathFromToolInput(in.ToolInput)
	if path == "" {
		return nil
	}

	cwd := in.CWD
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	proj := ""
	if cwd != "" {
		proj = filepath.Base(cwd)
	}

	d, err := loadDeps(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook post-tool:", err)
		return nil
	}
	defer d.close()

	// Best-effort session stamp. If nothing is open we still record the
	// touch so the heat map does not lose data; SessionID just stays empty.
	var sessID string
	if sess, err := d.sess.Current(ctx, ""); err == nil && sess != nil {
		sessID = sess.ID
	} else if err != nil && !errors.Is(err, session.ErrNotFound) {
		fmt.Fprintln(os.Stderr, "mnemos hook post-tool:", err)
	}

	if err := d.db.Touches().Record(ctx, memory.TouchInput{
		Project:   proj,
		Path:      path,
		SessionID: sessID,
		Note:      "auto:" + in.ToolName,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook post-tool:", err)
	}
	return nil
}

// isFileEditTool reports whether the Claude Code tool name is one we want
// to record as a file touch. Kept as a set so adding new editing tools is
// a one-liner.
func isFileEditTool(name string) bool {
	switch name {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return true
	}
	return false
}

// filePathFromToolInput extracts the file_path field common to Edit, Write,
// MultiEdit, and NotebookEdit. Returns "" if absent or not a string.
func filePathFromToolInput(m map[string]any) string {
	if m == nil {
		return ""
	}
	if v, ok := m["file_path"].(string); ok {
		return v
	}
	return ""
}

// runHookSessionEnd handles Claude Code's SessionEnd event. If a mnemos
// session is still open, close it with a summary stitched from recent
// activity and a status derived from the reason Claude Code supplied.
// No-ops when the agent already called mnemos_session_end properly —
// session.Close guards on ended_at IS NULL and returns ErrNotFound.
func runHookSessionEnd(ctx context.Context, _ []string) error {
	in := readHookStdin(os.Stdin)

	d, err := loadDeps(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook session-end:", err)
		return nil
	}
	defer d.close()

	sess, err := d.sess.Current(ctx, "")
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil
		}
		fmt.Fprintln(os.Stderr, "mnemos hook session-end:", err)
		return nil
	}
	if sess == nil {
		return nil
	}

	summary := deriveSessionSummary(ctx, d, sess.ID)
	status := sessionStatusFromReason(in.Reason)

	err = d.sess.Close(ctx, session.CloseInput{
		ID:          sess.ID,
		Summary:     summary,
		Status:      status,
		OutcomeTags: []string{"auto-closed:" + sanitizeReason(in.Reason)},
	})
	if err != nil && !errors.Is(err, session.ErrNotFound) {
		fmt.Fprintln(os.Stderr, "mnemos hook session-end:", err)
	}
	return nil
}

// deriveSessionSummary builds a one-line recap from recent activity on the
// session: counts observations and touches, names the top files. Best
// effort — returns "" if either query fails. A small, deterministic
// summary beats an empty close.
func deriveSessionSummary(ctx context.Context, d *deps, sessID string) string {
	obs, err := d.db.Observations().ListBySession(ctx, sessID)
	if err != nil {
		return ""
	}
	parts := []string{}
	if n := len(obs); n > 0 {
		parts = append(parts, fmt.Sprintf("%d observation(s)", n))
	}
	if hot, err := d.db.Touches().Hot(ctx, "", "", 5); err == nil && len(hot) > 0 {
		files := make([]string, 0, len(hot))
		for _, h := range hot {
			files = append(files, filepath.Base(h.Path))
		}
		parts = append(parts, "touched "+strings.Join(files, ", "))
	}
	if len(parts) == 0 {
		return "auto-closed on SessionEnd (no activity recorded)"
	}
	return "auto-closed on SessionEnd: " + strings.Join(parts, "; ")
}

// sessionStatusFromReason maps Claude Code's SessionEnd `reason` to a
// mnemos session status. Ctrl+C style exits get StatusAbandoned so
// replay/learning can weight them differently from clean closes.
func sessionStatusFromReason(reason string) session.Status {
	switch reason {
	case "prompt_input_exit":
		return session.StatusAbandoned
	case "bypass_permissions_disabled":
		return session.StatusBlocked
	default:
		return session.StatusOK
	}
}

// sanitizeReason defaults to "unknown" when Claude Code omitted the field
// so outcome tags stay queryable.
func sanitizeReason(r string) string {
	if r == "" {
		return "unknown"
	}
	return r
}
