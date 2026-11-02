package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/experiment"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
)

// runExperiment reports the randomised comparison between the push and index
// arms. Unlike efficacy, which can only correlate, the arms here were assigned
// by coin flip, so a difference can be credited to the retrieval method.
func runExperiment(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("experiment", flag.ContinueOnError)
	since := fs.Duration("since", 30*24*time.Hour, "look-back window (Go duration: 336h is two weeks)")
	format := fs.String("format", "text", "text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("invalid format: %s", *format)
	}
	if *since <= 0 {
		return fmt.Errorf("--since must be positive, got %s", *since)
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	assigned, err := d.db.AssignedConversations(ctx, time.Now().UTC().Add(-*since))
	if err != nil {
		return err
	}
	titles, err := liveTitles(ctx, d)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	roots := transcriptRoots(os.Getenv("CLAUDE_CONFIG_DIR"), home)

	conversations := make([]experiment.Conversation, 0, len(assigned))
	for _, a := range assigned {
		c := experiment.Conversation{HostID: a.HostID, Arm: a.Arm, Project: a.Project}
		if path := findTranscript(roots, a.HostID); path != "" {
			if metrics, err := measureTranscript(path, titles); err == nil {
				c.Metrics, c.Found = metrics, true
			}
		}
		conversations = append(conversations, c)
	}
	report := experiment.Build(conversations)

	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	renderExperiment(os.Stdout, report, d.cfg.Retrieval.Mode, *since)
	return nil
}

// liveTitles lists the titles the mention measure looks for.
func liveTitles(ctx context.Context, d *deps) ([]string, error) {
	all, err := d.db.Observations().ListByProject(ctx, "", "", "", 500)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	titles := make([]string, 0, len(all))
	for _, o := range all {
		if o.TrustTier != memory.TrustRaw {
			titles = append(titles, o.Title)
		}
	}
	return titles, nil
}

// transcriptRoots lists config directories that may hold a transcript. Every
// ~/.claude* directory counts, since one store can serve several accounts.
func transcriptRoots(claudeConfigDir, home string) []string {
	var roots []string
	if claudeConfigDir != "" {
		roots = append(roots, claudeConfigDir)
	}
	if home != "" {
		if matches, err := filepath.Glob(filepath.Join(home, ".claude*")); err == nil {
			roots = append(roots, matches...)
		}
	}
	return roots
}

// findTranscript locates a conversation's JSONL file by its host session ID.
func findTranscript(roots []string, hostID string) string {
	// The ID arrived in a hook payload and is about to become part of a glob.
	if hostID == "" || strings.ContainsAny(hostID, `/\*?[`) {
		return ""
	}
	for _, root := range roots {
		matches, err := filepath.Glob(filepath.Join(root, "projects", "*", hostID+".jsonl"))
		if err == nil && len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

func measureTranscript(path string, titles []string) (experiment.Metrics, error) {
	f, err := os.Open(path)
	if err != nil {
		return experiment.Metrics{}, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()
	return experiment.ReadTranscript(f, experiment.Options{
		IsSynthetic: isSyntheticPrompt,
		Titles:      titles,
	})
}

// renderExperiment writes the report. Pure function of its inputs.
func renderExperiment(w io.Writer, r experiment.Report, mode string, window time.Duration) {
	fmt.Fprintf(w, "mnemos experiment: push vs index, last %s (retrieval mode: %s)\n\n", formatWindow(window), mode)
	if r.Push.Assigned == 0 && r.Index.Assigned == 0 {
		if mode == session.ModeExperiment {
			fmt.Fprintln(w, "no conversations assigned in this window yet. New Claude Code conversations get an arm when they start.")
			return
		}
		fmt.Fprintln(w, "no conversations assigned in this window. Turn it on in the config:")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  [retrieval]")
		fmt.Fprintln(w, `  mode = "experiment"`)
		return
	}

	fmt.Fprintf(w, "%-46s %8s %8s\n", "", "push", "index")
	fmt.Fprintf(w, "%-46s %8d %8d\n", "conversations assigned", r.Push.Assigned, r.Index.Assigned)
	fmt.Fprintf(w, "%-46s %8d %8d\n", "with a transcript and a typed prompt", r.Push.Measured, r.Index.Measured)
	fmt.Fprintf(w, "%-46s %8d %8d\n", "prompts", r.Push.Totals.Prompts, r.Index.Totals.Prompts)
	fmt.Fprintf(w, "%-46s %8d %8d\n\n", "auto memory note reads", r.Push.Totals.NoteReads, r.Index.Totals.NoteReads)

	for _, c := range r.Comparisons {
		label := c.Name
		if c.Primary {
			label += " (primary)"
		}
		fmt.Fprintf(w, "%s\n", label)
		fmt.Fprintf(w, "  push %.2f (n=%d), index %.2f (n=%d)\n", c.Push, c.PushN, c.Index, c.IndexN)
		if c.PushN > 1 && c.IndexN > 1 {
			fmt.Fprintf(w, "  difference %+.2f, 95%% interval %+.2f to %+.2f\n", c.Diff, c.Low, c.High)
		}
	}

	fmt.Fprintf(w, "\n%s\n", r.Verdict)
	fmt.Fprintln(w, "\nnote: conversations were assigned at random, so a clear difference can be")
	fmt.Fprintln(w, "credited to the retrieval method. Mentions are title words appearing together in")
	fmt.Fprintln(w, "a reply, including some coincidences, which land on both arms alike. Pushback is")
	fmt.Fprintln(w, "rare and detected from wording, so it can warn about a large change and little")
	fmt.Fprintln(w, "else. The other measures are context, not tests.")
}
