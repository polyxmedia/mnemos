package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/safety"
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
	case "pre-tool":
		return runHookPreTool(ctx, rest)
	case "pre-compact":
		return runHookPreCompact(ctx, rest)
	case "post-compact":
		return runHookPostCompact(ctx, rest)
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

// runHookPreTool handles Claude Code's PreToolUse event for mnemos write
// tools. Runs the safety scanner over the tool_input payload; if an
// elevated-risk prompt-injection pattern is detected, exits 2 with a
// stderr message that Claude Code feeds back into the model's context.
// Claude receives a specific reason to revise and the save never lands.
//
// This is mnemos defending its own writes — a slice of Bet 2's quarantined
// tool-output tier, shipping at the hook layer well before the full
// provenance work. Pattern composes: future PreToolUse hooks (correction-
// collision on Edit, git-commit attribution, etc.) slot into the same
// subcommand, same exit-code contract.
func runHookPreTool(ctx context.Context, _ []string) error {
	_ = ctx
	msg, block := decidePreTool(readHookStdin(os.Stdin))
	if !block {
		return nil
	}
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(2)
	return nil
}

// decidePreTool is the pure-function core of the PreToolUse guardrail.
// Returns the stderr message to emit and whether Claude Code should be
// told to block (exit 2). Extracted so tests can assert both paths
// without spawning the binary just to observe an exit code.
func decidePreTool(in hookInput) (string, bool) {
	if !isMnemosWriteTool(in.ToolName) {
		return "", false
	}
	text := gatherStringsFromToolInput(in.ToolInput)
	if text == "" {
		return "", false
	}
	report := safety.NewScanner().Scan(text)
	if report.MaxRisk < safety.RiskHigh {
		return "", false
	}
	rules := uniqueRuleNames(report.Findings)
	return fmt.Sprintf(
		"mnemos: blocked %s — detected prompt-injection pattern (risk=%s; rules: %s). Remove the flagged text or rephrase before retrying.",
		shortToolName(in.ToolName), report.MaxRisk.String(), strings.Join(rules, ", "),
	), true
}

// isMnemosWriteTool matches the MCP-namespaced names Claude Code assigns
// to our write tools. The PreToolUse matcher we install already narrows
// Claude's invocation, but the defensive check keeps this command safe
// to invoke from elsewhere (tests, future shared guardrails).
func isMnemosWriteTool(name string) bool {
	switch name {
	case "mcp__mnemos__mnemos_save",
		"mcp__mnemos__mnemos_correct",
		"mcp__mnemos__mnemos_convention":
		return true
	}
	return false
}

// shortToolName strips the mcp__mnemos__ prefix for user-facing messages.
func shortToolName(name string) string {
	return strings.TrimPrefix(name, "mcp__mnemos__")
}

// gatherStringsFromToolInput flattens every string value in the tool_input
// map (including strings inside list values) into one scannable blob. The
// scanner looks for structural patterns, not natural language, so which
// field a match came from does not matter — we want total coverage and
// zero need to update this when the MCP tool schema gains a field.
func gatherStringsFromToolInput(m map[string]any) string {
	if m == nil {
		return ""
	}
	var out []string
	// Stable order for deterministic tests.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		walkStrings(m[k], &out)
	}
	return strings.Join(out, "\n")
}

// walkStrings appends every string found in v (a scalar, a slice, or a
// nested map) to dst. Non-string scalars are ignored.
func walkStrings(v any, dst *[]string) {
	switch x := v.(type) {
	case string:
		if x != "" {
			*dst = append(*dst, x)
		}
	case []any:
		for _, e := range x {
			walkStrings(e, dst)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkStrings(x[k], dst)
		}
	}
}

// runHookPreCompact handles Claude Code's PreCompact event. Emits a
// prewarm recovery block to stderr, which Claude Code feeds back into
// the model's context — so mnemos state survives through the compaction
// instead of being silently dropped. Without this, the agent keeps
// working for at least one more turn on context that has lost all
// mnemos observations, touches, and session goal.
func runHookPreCompact(ctx context.Context, _ []string) error {
	emitCompactionRecoveryBlock(ctx, os.Stderr, "PreCompact")
	return nil
}

// runHookPostCompact handles Claude Code's PostCompact event. Emits the
// same block to stderr for terminal visibility; unlike PreCompact,
// PostCompact stderr is shown to the user only (not fed back to Claude).
// The side effect a future Claude Code release might surface it; today
// the primary value is a transcript-level record that compaction
// happened and what mnemos looked like at that moment.
func runHookPostCompact(ctx context.Context, _ []string) error {
	emitCompactionRecoveryBlock(ctx, os.Stderr, "PostCompact")
	return nil
}

// emitCompactionRecoveryBlock composes a prewarm block in the compaction-
// recovery mode and writes it to w with a header naming the event. w is
// injected so tests can capture the output without going through stderr.
// Failure is always silent at the caller: a hook must not fail loudly.
func emitCompactionRecoveryBlock(ctx context.Context, w io.Writer, event string) {
	in := readHookStdin(os.Stdin)

	d, err := loadDeps(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook "+strings.ToLower(event)+":", err)
		return
	}
	defer d.close()

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

	var sessID string
	if sess, err := d.sess.Current(ctx, ""); err == nil && sess != nil {
		sessID = sess.ID
	} else if err != nil && !errors.Is(err, session.ErrNotFound) {
		fmt.Fprintln(os.Stderr, "mnemos hook "+strings.ToLower(event)+":", err)
	}

	pw := prewarm.NewService(prewarm.Config{
		Observations: d.db.Observations(),
		Sessions:     d.db.Sessions(),
		Skills:       d.db.Skills(),
		Touches:      d.db.Touches(),
		Rumination:   d.rum,
	})
	block, err := pw.Build(ctx, prewarm.Request{
		Mode:      prewarm.ModeCompactionRecovery,
		Project:   proj,
		SessionID: sessID,
	})
	if err != nil || block == nil || block.Text == "" {
		return
	}

	fmt.Fprintf(w, "[mnemos %s — recovery block]\n%s\n", event, block.Text)
}

// uniqueRuleNames deduplicates the rule names from the findings while
// keeping order stable for reproducible stderr messages.
func uniqueRuleNames(findings []safety.Finding) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range findings {
		if seen[f.Rule] {
			continue
		}
		seen[f.Rule] = true
		out = append(out, f.Rule)
	}
	return out
}
