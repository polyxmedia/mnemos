package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

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
