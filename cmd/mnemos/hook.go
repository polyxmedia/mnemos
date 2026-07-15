package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/safety"
	"github.com/polyxmedia/mnemos/internal/session"
)

// projectFromHook derives the project name from the hook payload's cwd,
// falling back to the process working directory. Same derivation the
// prewarm command uses, so SessionStart and SessionEnd agree on which
// project a session belongs to.
func projectFromHook(in hookInput) string {
	cwd := in.CWD
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

// runHook is the parent dispatcher for harness-side hook subcommands. Each
// leaf command reads a Claude Code hook payload from stdin (shape documented
// at https://code.claude.com/docs/en/hooks) and performs one invisible,
// best-effort side effect against the mnemos store. Failure is always silent
// at exit 0 — hooks must never block the user or spam the transcript.
//
// Honors MNEMOS_DISABLED=1 as a global kill-switch: the verify harness sets
// this in the off-arm so the user's globally-installed mnemos hooks (which
// fire regardless of --strict-mcp-config) cannot contaminate the off
// transcript with prewarm-injected memory or auto-search blocks.
func runHook(ctx context.Context, args []string) error {
	if os.Getenv("MNEMOS_DISABLED") != "" {
		return nil
	}
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

// runHookUserPrompt handles Claude Code's UserPromptSubmit event. Two
// invisible side effects:
//  1. Backfill the open session's goal if empty (one-shot, idempotent).
//  2. Search mnemos for relevant prior memory and inject the top hits to
//     stdout so Claude Code adds them to the model's context — bypassing
//     the agent's choice to (or not to) call mnemos_search itself. The
//     correction `agent skipped mnemos_session_start on editing tasks`
//     was saved precisely because LLMs skip optional tool calls; this
//     promotes mnemos from optional to ambient.
//
// Both effects degrade silently. UserPromptSubmit stdout is injected into
// the model context, so a failed search must produce zero stdout.
func runHookUserPrompt(ctx context.Context, _ []string) error {
	in := readHookStdin(os.Stdin)
	if in.Prompt == "" {
		return nil
	}

	d, err := loadDeps(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook user-prompt:", err)
		return nil
	}
	defer d.close()

	sess, err := d.sess.Current(ctx, "")
	switch {
	case err != nil && !errors.Is(err, session.ErrNotFound):
		fmt.Fprintln(os.Stderr, "mnemos hook user-prompt:", err)
	case sess != nil && sess.Goal == "":
		if err := d.sess.SetGoalIfEmpty(ctx, sess.ID, truncateGoal(in.Prompt)); err != nil {
			fmt.Fprintln(os.Stderr, "mnemos hook user-prompt:", err)
		}
	}

	var sessID, agentID string
	if sess != nil {
		sessID, agentID = sess.ID, sess.AgentID
	}
	emitPromptMemoryBlock(ctx, os.Stdout, d, in.Prompt, agentID, projectFromHook(in), sessID)
	emitCaptureDirective(os.Stdout, in.Prompt)
	return nil
}

// captureSignal names what kind of write tool the user's phrasing is
// asking for. Returned by detectCaptureShape; consumed by
// emitCaptureDirective.
type captureSignal int

const (
	captureNone captureSignal = iota
	captureCorrection
	captureConvention
	captureSave
)

// detectCaptureShape pattern-matches the user's prompt for phrasing that
// should fire a write tool. Description-level hints (in the MCP tool
// docs) ceiling out around 13% capture in claude -p — the agent stays
// task-focused and skips optional tool calls. This adds a directive
// block to the prompt context that's much harder to skip.
//
// Patterns are intentionally conservative — we want low false-positive
// rate, not maximal recall. Better to miss a borderline correction than
// nag the agent on every routine prompt.
func detectCaptureShape(prompt string) captureSignal {
	p := strings.ToLower(prompt)

	// Convention: explicit project rules. Highest precedence — "we always
	// do X" is a rule, not a one-off correction.
	conventionPatterns := []string{
		"we always", "we never",
		"in this codebase we", "in this repo we", "in this project we",
		"the convention here is", "the convention is",
		"by convention", "the rule is", "the rule here is",
		"every error", "every commit", "every migration",
	}
	for _, pat := range conventionPatterns {
		if strings.Contains(p, pat) {
			return captureConvention
		}
	}

	// Correction: tried/wrong/fix shape, including past-failure references.
	correctionPatterns := []string{
		"we tried", "we were wrong",
		"going forward", "from now on",
		"don't do", "do not do", "don't use", "do not use",
		"that's wrong", "that is wrong",
		"actually wait", "hold on",
		"caused the bug", "caused a bug",
		"last time we", "last quarter we", "last week we",
		"don't ever", "never do",
	}
	for _, pat := range correctionPatterns {
		if strings.Contains(p, pat) {
			return captureCorrection
		}
	}

	// Save: explicit memory request or architectural-decision narration.
	savePatterns := []string{
		"save this", "remember this", "remember that",
		"note this", "note that", "for future sessions",
		"keep this for next", "we just decided",
		"we're going with", "we are going with", "going with",
	}
	for _, pat := range savePatterns {
		if strings.Contains(p, pat) {
			return captureSave
		}
	}

	return captureNone
}

// emitCaptureDirective writes a strong-language block to w when the
// user's prompt looks like a correction, convention, or save request.
// The block sits in the agent's system prompt context (UserPromptSubmit
// stdout is concatenated in by Claude Code) and tells the agent which
// tool to call before doing anything else.
//
// We do NOT call the tool ourselves — the agent owns the structured
// arguments (tried/wrong_because/fix), and we want the agent to learn
// the pattern, not be replaced by it. The directive raises capture from
// "optional" to "non-optional in this turn."
func emitCaptureDirective(w io.Writer, prompt string) {
	signal := detectCaptureShape(prompt)
	if signal == captureNone {
		return
	}
	switch signal {
	case captureCorrection:
		fmt.Fprintln(w, "[mnemos: capture required] The user's prompt contains correction-shaped phrasing (\"we tried X\", \"going forward\", \"don't do Y\", or similar). BEFORE answering the rest of the prompt, call mnemos_correct with: tried = the failed approach, wrong_because = why it failed, fix = what to do instead, trigger_context = a phrase that should resurface this in future. Do this even if it interrupts the task — the next session will repeat the mistake otherwise.")
	case captureConvention:
		fmt.Fprintln(w, "[mnemos: capture required] The user's prompt declares a project convention (\"we always X\", \"the rule here is Y\", or similar). BEFORE answering the rest of the prompt, call mnemos_convention with title = short label, rule = the rule itself, rationale = why. The next agent on this project should not have to be told this twice.")
	case captureSave:
		fmt.Fprintln(w, "[mnemos: capture required] The user explicitly asked you to remember or save something, OR narrated an architectural decision (\"we just decided X\", \"going with Y\"). BEFORE answering the rest of the prompt, search mnemos first; if not already recorded, call mnemos_save with type = decision (or convention if it's a project rule), title = short label, content = the substance.")
	}
}

// promptMemoryMinScore is the BM25-after-ranker score under which we
// suppress injection. Tuned empirically against the seed store: hits at
// 1.5+ are clearly on-topic; below that we'd be force-feeding noise into
// every prompt's context window.
const promptMemoryMinScore = 1.5

// promptMemoryMaxHits caps how many memories we inject per prompt. Three
// is small enough to stay invisible on routine prompts but large enough to
// catch the "convention + correction + decision" cluster around a topic.
const promptMemoryMaxHits = 3

// emitPromptMemoryBlock searches mnemos for the user's prompt and writes a
// compact context block to w when at least one hit clears the score floor.
// Best-effort: every error path produces zero stdout so a hook bug never
// poisons the agent's context. Kept hits are recorded in the injection log
// (channel prompt_hook) so surfaced-vs-used ratios cover this path too.
func emitPromptMemoryBlock(ctx context.Context, w io.Writer, d *deps, prompt, agentID, project, sessionID string) {
	if prompt == "" || d == nil {
		return
	}
	// PreferProject, not Project: a hard filter would drop project-less
	// global conventions. Soft affinity downranks other projects' memories
	// so they only surface when strongly on-topic.
	hits, err := d.mem.Search(ctx, memory.SearchInput{
		Query:         prompt,
		PreferProject: project,
		Limit:         promptMemoryMaxHits,
	})
	if err != nil || len(hits) == 0 {
		return
	}
	kept := hits[:0]
	for _, h := range hits {
		if h.Score >= promptMemoryMinScore {
			kept = append(kept, h)
		}
	}
	if len(kept) == 0 {
		return
	}
	fmt.Fprintln(w, "[mnemos: prior memory relevant to this prompt — apply or address]")
	refs := make([]injection.Ref, 0, len(kept))
	for _, h := range kept {
		title := h.Observation.Title
		if title == "" {
			title = h.Observation.ID
		}
		snippet := strings.TrimSpace(h.Snippet)
		if snippet == "" {
			snippet = strings.TrimSpace(h.Observation.Content)
		}
		if len(snippet) > 240 {
			snippet = snippet[:237] + "…"
		}
		fmt.Fprintf(w, "- [%s] %s — %s\n", h.Observation.Type, title, snippet)
		refs = append(refs, injection.Ref{Kind: injection.KindObservation, ID: h.Observation.ID})
	}
	_ = injection.NewLogger(d.db.Injections(), nil).
		Log(ctx, injection.ChannelPromptHook, agentID, project, sessionID, refs)
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

	proj := projectFromHook(in)

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

// runHookSessionEnd handles Claude Code's SessionEnd event. Closes every
// open mnemos session for the project (SessionStart hooks installed at
// both user and project scope each open one, so there can be twins) with
// a summary stitched from recent activity and a status derived from the
// reason Claude Code supplied. No-ops when the agent already called
// mnemos_session_end properly — session.Close guards on ended_at IS NULL.
// Finishes with a stale sweep so sessions orphaned by killed terminals
// (no SessionEnd ever fires) don't stay open forever.
func runHookSessionEnd(ctx context.Context, _ []string) error {
	in := readHookStdin(os.Stdin)

	d, err := loadDeps(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook session-end:", err)
		return nil
	}
	defer d.close()

	proj := projectFromHook(in)
	status := sessionStatusFromReason(in.Reason)
	tags := []string{"auto-closed:" + sanitizeReason(in.Reason)}

	if proj == "" {
		// No project derivable: closing all open sessions would hit other
		// projects' live sessions, so fall back to the newest one only.
		if sess, err := d.sess.Current(ctx, ""); err == nil && sess != nil {
			closeSessionQuietly(ctx, d, session.CloseInput{
				ID: sess.ID, Summary: deriveSessionSummary(ctx, d, sess.ID),
				Status: status, OutcomeTags: tags,
			})
		} else if err != nil && !errors.Is(err, session.ErrNotFound) {
			fmt.Fprintln(os.Stderr, "mnemos hook session-end:", err)
		}
		sweepStaleSessions(ctx, d)
		return nil
	}

	open, err := d.sess.ListOpen(ctx, proj)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook session-end:", err)
		return nil
	}
	for _, sess := range open {
		closeSessionQuietly(ctx, d, session.CloseInput{
			ID: sess.ID, Summary: deriveSessionSummary(ctx, d, sess.ID),
			Status: status, OutcomeTags: tags,
		})
	}

	sweepStaleSessions(ctx, d)
	return nil
}

// closeSessionQuietly closes one session, logging anything except the
// benign already-closed race to stderr. Hooks never fail loudly.
func closeSessionQuietly(ctx context.Context, d *deps, in session.CloseInput) {
	if err := d.sess.Close(ctx, in); err != nil && !errors.Is(err, session.ErrNotFound) {
		fmt.Fprintln(os.Stderr, "mnemos hook session-end:", err)
	}
}

// staleSessionMaxAge is how long a session may stay open before the sweep
// declares it abandoned. SessionEnd never fires for killed terminals or
// crashed hosts, so without the sweep those sessions hold ended_at NULL
// forever and pollute Current()/ListOpen() for every later hook.
const staleSessionMaxAge = 24 * time.Hour

// sweepStaleSessions closes open sessions older than staleSessionMaxAge
// across all projects as abandoned. Runs piggybacked on SessionEnd — the
// next clean exit anywhere tidies the whole store.
func sweepStaleSessions(ctx context.Context, d *deps) {
	open, err := d.sess.ListOpen(ctx, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook session-end:", err)
		return
	}
	cutoff := time.Now().UTC().Add(-staleSessionMaxAge)
	for _, sess := range open {
		if !sess.StartedAt.Before(cutoff) {
			continue
		}
		closeSessionQuietly(ctx, d, session.CloseInput{
			ID:          sess.ID,
			Summary:     "auto-closed as stale: open longer than 24h with no SessionEnd (terminal killed or hook never fired)",
			Status:      session.StatusAbandoned,
			OutcomeTags: []string{"auto-closed:stale"},
		})
	}
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

// runHookPreTool handles Claude Code's PreToolUse event. Two surfaces in
// one subcommand, dispatched on tool_name:
//
//  1. Guardrail on mnemos write tools: runs the safety scanner over the
//     tool_input payload; an elevated-risk prompt-injection pattern exits
//     2 with stderr fed back to the model, and the save never lands.
//  2. Just-in-time memory on file-edit tools: searches corrections and
//     conventions relevant to the file about to be written and returns
//     them as PreToolUse additionalContext — the memory arrives at the
//     decision point, not 40 turns earlier in the session-start block.
func runHookPreTool(ctx context.Context, _ []string) error {
	in := readHookStdin(os.Stdin)
	msg, block := decidePreTool(in)
	if block {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(2)
	}
	if isFileEditTool(in.ToolName) {
		emitPreToolMemory(ctx, os.Stdout, in)
	}
	return nil
}

// preToolMemoryMaxHits caps how many memories ride along on one edit.
// Same rationale as the prompt hook's cap: invisible on routine edits,
// enough to carry the correction + convention cluster around a file.
const preToolMemoryMaxHits = 3

// emitPreToolMemory searches the store for corrections and conventions
// relevant to the file a PreToolUse(Edit|Write) event is about to touch
// and emits them as hookSpecificOutput.additionalContext JSON on stdout.
// No permissionDecision is ever emitted — this surface must never
// auto-approve or block a write, only inform it. Memories already
// surfaced in the current session (any channel, read from the injection
// log) are skipped, so repeated edits to the same file don't spam the
// context with the same warning. Best-effort: every failure path emits
// nothing.
func emitPreToolMemory(ctx context.Context, w io.Writer, in hookInput) {
	path := filePathFromToolInput(in.ToolInput)
	if path == "" {
		return
	}
	query := pathQueryTokens(path)
	if query == "" {
		return
	}

	d, err := loadDeps(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook pre-tool:", err)
		return
	}
	defer d.close()

	hits, err := d.mem.Search(ctx, memory.SearchInput{
		Query:   query,
		Project: projectFromHook(in),
		Limit:   preToolMemoryMaxHits * 2,
	})
	if err != nil || len(hits) == 0 {
		return
	}

	var sessID, agentID string
	if sess, err := d.sess.Current(ctx, ""); err == nil && sess != nil {
		sessID, agentID = sess.ID, sess.AgentID
	}

	injStore := d.db.Injections()
	var kept []memory.SearchResult
	for _, h := range hits {
		if h.Observation.Type != memory.TypeCorrection && h.Observation.Type != memory.TypeConvention {
			continue
		}
		if h.Score < promptMemoryMinScore {
			continue
		}
		if alreadyInjectedInSession(ctx, injStore, h.Observation.ID, sessID) {
			continue
		}
		kept = append(kept, h)
		if len(kept) >= preToolMemoryMaxHits {
			break
		}
	}
	if len(kept) == 0 {
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[mnemos: memory relevant to %s — apply before editing]\n", filepath.Base(path))
	refs := make([]injection.Ref, 0, len(kept))
	for _, h := range kept {
		snippet := strings.TrimSpace(h.Snippet)
		if snippet == "" {
			snippet = strings.TrimSpace(h.Observation.Content)
		}
		if len(snippet) > 240 {
			snippet = snippet[:237] + "…"
		}
		fmt.Fprintf(&b, "- [%s] %s — %s\n", h.Observation.Type, h.Observation.Title, snippet)
		refs = append(refs, injection.Ref{Kind: injection.KindObservation, ID: h.Observation.ID})
	}

	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "PreToolUse",
			"additionalContext": strings.TrimRight(b.String(), "\n"),
		},
	}
	if err := json.NewEncoder(w).Encode(out); err != nil {
		return
	}
	_ = injection.NewLogger(injStore, nil).
		Log(ctx, injection.ChannelPreTool, agentID, projectFromHook(in), sessID, refs)
}

// pathQueryTokens turns a file path into a search query: the last few
// path segments split on separators, lowercased. "/Users/x/repo/internal/
// storage/sessions.go" → "internal storage sessions go". The FTS layer
// ORs the tokens, so any segment matching a correction's trigger context
// or content surfaces it.
func pathQueryTokens(p string) string {
	segs := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	if len(segs) > 4 {
		segs = segs[len(segs)-4:]
	}
	var toks []string
	for _, s := range segs {
		for _, t := range strings.FieldsFunc(s, func(r rune) bool {
			return r == '.' || r == '_' || r == '-'
		}) {
			if len(t) > 1 {
				toks = append(toks, strings.ToLower(t))
			}
		}
	}
	return strings.Join(toks, " ")
}

// alreadyInjectedInSession reports whether the observation was surfaced
// into the given session through any channel. Used as a spam guard:
// repeated edits to the same file should not re-inject the same memory.
func alreadyInjectedInSession(ctx context.Context, store injection.Store, refID, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	events, err := store.ListByRef(ctx, injection.KindObservation, refID, 50)
	if err != nil {
		return false
	}
	for _, e := range events {
		if e.SessionID == sessionID {
			return true
		}
	}
	return false
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

	proj := projectFromHook(in)

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
		Injections:   injection.NewLogger(d.db.Injections(), nil),
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
