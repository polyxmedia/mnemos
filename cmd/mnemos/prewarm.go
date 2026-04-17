package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
)

// hookInput mirrors the JSON payload Claude Code writes to the SessionStart
// hook's stdin. We tolerate missing fields: the command also runs standalone
// from a regular shell where stdin carries nothing.
type hookInput struct {
	SessionID     string `json:"session_id,omitempty"`
	CWD           string `json:"cwd,omitempty"`
	HookEventName string `json:"hook_event_name,omitempty"`
	Source        string `json:"source,omitempty"`
}

// runPrewarm composes a prewarm block and prints it. Designed to be wired as
// a Claude Code SessionStart hook so session_start context gets pushed without
// the agent having to call mnemos_session_start explicitly.
//
// Failure is intentionally quiet: if anything goes wrong (no config, DB
// unreachable, scanner error) we exit 0 with empty stdout. The hook must not
// block or spam the user; degraded-silent is the right failure mode.
func runPrewarm(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("prewarm", flag.ContinueOnError)
	var (
		project     = fs.String("project", "", "project name (default: basename of cwd)")
		goal        = fs.String("goal", "", "optional goal")
		mode        = fs.String("mode", "session_start", "session_start | compaction_recovery")
		format      = fs.String("format", "text", "text | json")
		openSession = fs.Bool("open-session", true, "open a mnemos session (session_start mode only)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	pwMode, err := parsePrewarmMode(*mode)
	if err != nil {
		return err
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("invalid format: %s", *format)
	}

	in := readHookStdin(os.Stdin)
	cwd := in.CWD
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	proj := *project
	if proj == "" && cwd != "" {
		proj = filepath.Base(cwd)
	}

	d, err := loadDeps(ctx)
	if err != nil {
		// Degraded silent: hook must not fail loudly.
		fmt.Fprintln(os.Stderr, "mnemos prewarm:", err)
		return nil
	}
	defer d.close()

	pw := prewarm.NewService(prewarm.Config{
		Observations: d.db.Observations(),
		Sessions:     d.db.Sessions(),
		Skills:       d.db.Skills(),
		Touches:      d.db.Touches(),
	})

	var sessID string
	if *openSession && pwMode == prewarm.ModeSessionStart {
		if sess, err := d.sess.Open(ctx, session.OpenInput{
			Project: proj, Goal: *goal,
		}); err == nil {
			sessID = sess.ID
		}
	} else if pwMode == prewarm.ModeCompactionRecovery {
		// For compaction recovery we need an existing session to scope to.
		// The Claude Code hook payload's session_id isn't the mnemos id; we
		// fall back to the most recent open session for this project.
		if sess, err := d.sess.Current(ctx, ""); err == nil && sess != nil {
			sessID = sess.ID
		}
	}

	block, err := pw.Build(ctx, prewarm.Request{
		Mode:      pwMode,
		Project:   proj,
		Goal:      *goal,
		SessionID: sessID,
	})
	if err != nil || block == nil {
		return nil
	}

	switch *format {
	case "json":
		out := map[string]any{
			"session_id":     sessID,
			"project":        proj,
			"mode":           *mode,
			"text":           block.Text,
			"token_estimate": block.TokenEstimate,
			"section_count":  len(block.Sections),
			"safety_risk":    block.SafetyReport.MaxRisk.String(),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case "text":
		if block.Text != "" {
			fmt.Println(block.Text)
		}
		if sessID != "" {
			fmt.Printf("\nmnemos_session_id: %s\n", sessID)
		}
	}
	return nil
}

func parsePrewarmMode(s string) (prewarm.Mode, error) {
	switch s {
	case "session_start", "":
		return prewarm.ModeSessionStart, nil
	case "compaction_recovery":
		return prewarm.ModeCompactionRecovery, nil
	default:
		return 0, fmt.Errorf("invalid mode: %s (want session_start | compaction_recovery)", s)
	}
}

// readHookStdin best-effort parses a Claude Code hook payload from stdin.
// Returns a zero value if stdin is a TTY, empty, or not valid JSON. Errors
// are swallowed on purpose: the CLI also runs interactively.
func readHookStdin(r io.Reader) hookInput {
	var in hookInput
	f, ok := r.(*os.File)
	if ok {
		fi, err := f.Stat()
		if err != nil {
			return in
		}
		if (fi.Mode() & os.ModeCharDevice) != 0 {
			// Interactive TTY — nothing to read.
			return in
		}
	}
	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return in
	}
	// Ignore unmarshal errors: a non-JSON stdin is a legitimate shell use.
	var decodeErr *json.SyntaxError
	if err := json.Unmarshal(data, &in); err != nil && !errors.As(err, &decodeErr) {
		return in
	}
	return in
}
