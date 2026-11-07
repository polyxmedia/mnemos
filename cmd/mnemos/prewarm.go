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
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// hookInput mirrors the JSON payload Claude Code writes to hook stdin.
// Fields are a superset across SessionStart, UserPromptSubmit, Stop, and
// PostToolUse — missing fields are tolerated, since each hook populates
// only what its event carries, and the command also runs standalone from
// a regular shell where stdin carries nothing.
type hookInput struct {
	SessionID     string         `json:"session_id,omitempty"`
	CWD           string         `json:"cwd,omitempty"`
	HookEventName string         `json:"hook_event_name,omitempty"`
	Source        string         `json:"source,omitempty"`
	Prompt        string         `json:"prompt,omitempty"`
	ToolName      string         `json:"tool_name,omitempty"`
	ToolInput     map[string]any `json:"tool_input,omitempty"`
	Reason        string         `json:"reason,omitempty"`
}

// runPrewarm composes a prewarm block and prints it. Designed to be wired as
// a Claude Code SessionStart hook so session_start context gets pushed without
// the agent having to call mnemos_session_start explicitly.
//
// Failure is intentionally quiet: if anything goes wrong (no config, DB
// unreachable, scanner error) we exit 0 with empty stdout. The hook must not
// block or spam the user; degraded-silent is the right failure mode.
func runPrewarm(ctx context.Context, args []string) error {
	if os.Getenv("MNEMOS_DISABLED") != "" {
		return nil
	}
	fs := flag.NewFlagSet("prewarm", flag.ContinueOnError)
	var (
		project     = fs.String("project", "", "project name (default: the git repo of cwd, else its basename)")
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
	if proj == "" {
		proj = projectFromCWD(cwd)
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
		Rumination:   d.rum,
		Injections:   injection.NewLogger(d.db.Injections(), nil),
	})

	var sessID string
	var arm session.RetrievalArm
	if *openSession && pwMode == prewarm.ModeSessionStart {
		if sess := adoptableOpenSession(ctx, d, in.SessionID, proj); sess != nil {
			sessID, arm = sess.ID, sess.RetrievalArm
		} else if sess, err := d.sess.Open(ctx, session.OpenInput{
			Project: proj, Goal: *goal, HostSessionID: in.SessionID,
			RetrievalArm: assignArm(d.cfg.Retrieval.Mode, in.SessionID, cwd),
		}); err == nil {
			sessID, arm = sess.ID, sess.RetrievalArm
		}
	} else if pwMode == prewarm.ModeCompactionRecovery {
		// Recovery restores the state of the session that was compacted:
		// the one SessionStart opened for this host conversation.
		if sess, err := d.sess.Resolve(ctx, in.SessionID, proj); err == nil && sess != nil {
			sessID, arm = sess.ID, sess.RetrievalArm
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

	// The index goes out after compaction too: it is the only way this arm
	// reaches memory, and a compacted context has lost the first copy.
	var memoryIndex string
	if arm == session.ArmIndex {
		if text, err := pw.MemoryIndex(ctx, "", proj); err == nil {
			memoryIndex = text
		}
	}

	channel := storage.EventPrewarm
	if pwMode == prewarm.ModeCompactionRecovery {
		channel = storage.EventRecovery
	}
	chars := len(block.Text) + len(memoryIndex)
	recordRetrievalEvent(ctx, d, cwd, storage.RetrievalEvent{
		Channel: channel, HostSessionID: in.SessionID, SessionID: sessID, Project: proj,
		Arm: string(arm), Fired: chars > 0, Chars: chars,
	})

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
			"memory_index":   memoryIndex,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case "text":
		if block.Text != "" {
			fmt.Println(block.Text)
		}
		if memoryIndex != "" {
			fmt.Printf("\n%s\n", memoryIndex)
		}
		if sessID != "" {
			fmt.Printf("\nmnemos_session_id: %s\n", sessID)
		}
	}
	return nil
}

// assignArm picks a session's retrieval arm, keeping conversations that run
// inside Claude Code's own temp area (tmp/claude-<uid>/...) out of the
// experiment. Those are scripted runs, such as one session's end-to-end
// tests driving the claude CLI from its scratchpad: in the first week of
// the experiment they were 10 of its 51 conversations, each a one-line
// prompt with nothing to remember. Every one of them ran there.
func assignArm(mode, hostSessionID, cwd string) session.RetrievalArm {
	if isClaudeTempWorkDir(cwd) {
		return ""
	}
	return session.AssignArm(mode, hostSessionID)
}

// isClaudeTempWorkDir reports whether dir lies under tmp/claude-<uid>.
func isClaudeTempWorkDir(dir string) bool {
	if dir == "" {
		return false
	}
	segs := strings.FieldsFunc(filepath.ToSlash(dir), func(r rune) bool { return r == '/' })
	return hasClaudeTempDir(segs)
}

// sessionAdoptWindow bounds how recently an open session must have started
// for a new SessionStart firing to adopt it instead of opening a twin.
// SessionStart hooks installed at both user and project scope both fire
// within milliseconds of each other; before this guard every Claude
// session on such a setup left a duplicate mnemos session that never
// closed. The window is deliberately short so a genuinely new session an
// hour later still gets its own row.
const sessionAdoptWindow = 90 * time.Second

// adoptableOpenSession returns the session a SessionStart firing should
// reuse instead of opening a twin, or nil. With a host session ID only a
// session opened for that same host conversation qualifies, so a second
// terminal started in the same project inside the window still gets its
// own session. Without one it falls back to the newest open session for
// the project if it started within sessionAdoptWindow. Best-effort: lookup
// failures mean "open a fresh one".
func adoptableOpenSession(ctx context.Context, d *deps, hostSessionID, project string) *session.Session {
	if hostSessionID != "" {
		owned, err := d.sess.ListOpenByHost(ctx, hostSessionID)
		if err != nil || len(owned) == 0 {
			return nil
		}
		return &owned[0]
	}
	if project == "" {
		return nil
	}
	open, err := d.sess.ListOpen(ctx, project)
	if err != nil || len(open) == 0 {
		return nil
	}
	newest := open[0]
	if time.Since(newest.StartedAt) > sessionAdoptWindow {
		return nil
	}
	return &newest
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
