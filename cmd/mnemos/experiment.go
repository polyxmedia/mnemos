package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/experiment"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

const experimentUsage = `usage:
  mnemos experiment [--since D] [--format text|json]
                                   compare the push and index arms
  mnemos experiment sample [--n N] [--channel C] [--seed S] [--out FILE]
                                   export logged prompts and memories to label blind
  mnemos experiment score FILE     read a labelled sample back`

// runExperiment reports the randomised comparison between the push and index
// arms. Unlike efficacy, which can only correlate, the arms here were assigned
// by coin flip, so a difference can be credited to the retrieval method.
func runExperiment(ctx context.Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "sample":
			return runExperimentSample(ctx, os.Stdout, args[1:])
		case "score":
			return runExperimentScore(ctx, os.Stdout, args[1:])
		case "help", "-h", "--help":
			fmt.Println(experimentUsage)
			return nil
		}
	}
	fs := flag.NewFlagSet("experiment", flag.ContinueOnError)
	since := fs.Duration("since", 90*24*time.Hour, "look-back window, cut short by [retrieval] experiment_start (Go duration: 336h is two weeks)")
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

	from, err := experimentWindowStart(d.cfg.Retrieval.ExperimentStart, *since, time.Now().UTC())
	if err != nil {
		return err
	}
	assigned, err := d.db.AssignedConversations(ctx, from)
	if err != nil {
		return err
	}
	events, err := d.db.RetrievalEvents().Since(ctx, from)
	if err != nil {
		return err
	}
	delivery := deliveryByHost(events)
	titles, err := liveTitles(ctx, d)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	roots := transcriptRoots(os.Getenv("CLAUDE_CONFIG_DIR"), home)

	conversations := make([]experiment.Conversation, 0, len(assigned))
	for _, a := range assigned {
		c := experiment.Conversation{HostID: a.HostID, Arm: a.Arm, Project: a.Project, Delivery: delivery[a.HostID]}
		if path := findTranscript(roots, a.HostID); path != "" {
			if metrics, err := measureTranscript(path, titles); err == nil {
				c.Metrics, c.Found = metrics, true
				// Assignment skips these since 2026-11-07; older rows still
				// carry an arm.
				if isClaudeTempWorkDir(metrics.CWD) {
					c.Excluded = "scripted run in Claude Code's temp area"
				}
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
	renderExperiment(os.Stdout, report, d.cfg.Retrieval.Mode, from)
	return nil
}

// experimentWindowStart is the later of now minus since and the configured
// experiment_start.
func experimentWindowStart(start string, since time.Duration, now time.Time) (time.Time, error) {
	from := now.Add(-since)
	if strings.TrimSpace(start) == "" {
		return from, nil
	}
	t, err := parseExperimentStart(start)
	if err != nil {
		return time.Time{}, err
	}
	if t.After(from) {
		return t, nil
	}
	return from, nil
}

func parseExperimentStart(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("[retrieval] experiment_start %q: want a date like 2026-11-07 or an RFC 3339 time", s)
}

// deliveryByHost totals the event log per host conversation.
func deliveryByHost(events []storage.RetrievalEvent) map[string]experiment.Delivery {
	out := map[string]experiment.Delivery{}
	for _, e := range events {
		if e.HostSessionID == "" {
			continue
		}
		d := out[e.HostSessionID]
		switch e.Channel {
		case storage.EventPromptHook:
			d.PromptEvents++
			if e.Fired {
				d.PromptFires++
			}
		case storage.EventCue:
			d.CueFires++
		case storage.EventPreTool:
			if e.Fired {
				d.PreToolFires++
			}
		}
		d.Chars += e.Chars
		out[e.HostSessionID] = d
	}
	return out
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
func renderExperiment(w io.Writer, r experiment.Report, mode string, from time.Time) {
	fmt.Fprintf(w, "mnemos experiment: push vs index, conversations since %s (retrieval mode: %s)\n\n",
		from.Format("2006-01-02 15:04 UTC"), mode)
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

	row := func(label string, p, i any) { fmt.Fprintf(w, "%-46s %10v %10v\n", label, p, i) }
	pct := func(v float64) string { return fmt.Sprintf("%.0f%%", 100*v) }
	perPrompt := func(n int, s experiment.ArmSummary) string {
		if s.Totals.Prompts == 0 {
			return "-"
		}
		return fmt.Sprintf("%.0f", float64(n)/float64(s.Totals.Prompts))
	}
	p, i := r.Push, r.Index
	row("", "push", "index")
	row("conversations assigned", p.Assigned, i.Assigned)
	row("  left out (scripted runs)", p.Excluded, i.Excluded)
	row("  no transcript found", p.Missing, i.Missing)
	row("  measured (transcript and a typed prompt)", p.Measured, i.Measured)
	row("typed prompts", p.Totals.Prompts, i.Totals.Prompts)
	row("  with a prompt hook event", pct(p.HookCoverage), pct(i.HookCoverage))
	row("  in the two longest conversations", pct(p.TopTwoShare), pct(i.TopTwoShare))
	row("prompt hook showed memory", p.Delivery.PromptFires, i.Delivery.PromptFires)
	row("cues fired", p.Delivery.CueFires, i.Delivery.CueFires)
	row("pre-edit search showed memory", p.Delivery.PreToolFires, i.Delivery.PreToolFires)
	row("characters injected per prompt", perPrompt(p.Delivery.Chars, p), perPrompt(i.Delivery.Chars, i))
	row("auto memory note reads", p.Totals.NoteReads, i.Totals.NoteReads)
	fmt.Fprintln(w)

	for _, c := range r.Comparisons {
		fmt.Fprintf(w, "%s (%s)\n", c.Name, c.Role)
		fmt.Fprintf(w, "  push %.2f (n=%d), index %.2f (n=%d)\n", c.Push, c.PushN, c.Index, c.IndexN)
		if c.HasInterval {
			fmt.Fprintf(w, "  difference %+.2f, 95%% interval %+.2f to %+.2f\n", c.Diff, c.Low, c.High)
		}
	}

	if len(r.Problems) > 0 {
		fmt.Fprintln(w, "\ndata problems:")
		for _, pr := range r.Problems {
			fmt.Fprintf(w, "  - %s\n", pr)
		}
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintln(w, "\nwarnings:")
		for _, wr := range r.Warnings {
			fmt.Fprintf(w, "  - %s\n", wr)
		}
	}
	fmt.Fprintf(w, "\n%s\n%s\n", r.Verdict, r.Decision)
	fmt.Fprintln(w, "\nnote: conversations were assigned at random, so a clear difference can be")
	fmt.Fprintln(w, "credited to the retrieval method. Rates pool prompts across conversations and")
	fmt.Fprintln(w, "intervals resample whole conversations. Mentions are title words appearing")
	fmt.Fprintln(w, "together in a reply, including coincidences that land on both arms alike. The")
	fmt.Fprintln(w, "decision rule is fixed in docs/experiment.md; precision of what was shown comes")
	fmt.Fprintln(w, "from `mnemos experiment sample` and `score`.")
}

// runExperimentSample writes logged events and the memories they considered
// as JSONL pairs to label blind.
func runExperimentSample(ctx context.Context, stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("experiment sample", flag.ContinueOnError)
	n := fs.Int("n", 60, "events to sample")
	channel := fs.String("channel", storage.EventPromptHook, "prompt_hook | cue | pre_tool")
	seed := fs.Uint64("seed", 1, "sampling seed: the same seed and log give the same sample")
	since := fs.Duration("since", 90*24*time.Hour, "look-back window, cut short by [retrieval] experiment_start")
	out := fs.String("out", "", "file to write (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch *channel {
	case storage.EventPromptHook, storage.EventCue, storage.EventPreTool:
	default:
		return fmt.Errorf("invalid channel %q", *channel)
	}
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	from, err := experimentWindowStart(d.cfg.Retrieval.ExperimentStart, *since, time.Now().UTC())
	if err != nil {
		return err
	}
	events, err := labelEvents(ctx, d, from)
	if err != nil {
		return err
	}
	var pool []experiment.Event
	for _, e := range events {
		if e.Channel == *channel {
			pool = append(pool, e)
		}
	}
	texts := map[string]experiment.MemoryText{}
	for _, e := range pool {
		for _, id := range eventMemoryIDs(e) {
			if _, done := texts[id]; done {
				continue
			}
			if o, err := d.db.Observations().Get(ctx, id); err == nil && o != nil {
				texts[id] = experiment.MemoryText{Title: o.Title, Content: o.Content, Project: o.Project}
			}
		}
	}
	pairs := experiment.Sample(pool, texts, *n, *seed)

	w := stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return fmt.Errorf("create %s: %w", *out, err)
		}
		defer f.Close()
		w = f
	}
	enc := json.NewEncoder(w)
	for _, p := range pairs {
		if err := enc.Encode(p); err != nil {
			return fmt.Errorf("write sample: %w", err)
		}
	}
	if *out != "" {
		fmt.Fprintf(stdout, "wrote %d pairs from %d %s events to %s. Set \"relevant\" to true or false on each line, then run mnemos experiment score %s\n",
			len(pairs), min(*n, len(pool)), *channel, *out, *out)
	}
	return nil
}

func eventMemoryIDs(e experiment.Event) []string {
	ids := make([]string, 0, len(e.Candidates)+len(e.Injected))
	for _, c := range e.Candidates {
		ids = append(ids, c.ID)
	}
	return append(ids, e.Injected...)
}

// labelEvents loads logged events in the shape the labelling tools take.
func labelEvents(ctx context.Context, d *deps, from time.Time) ([]experiment.Event, error) {
	raw, err := d.db.RetrievalEvents().Since(ctx, from)
	if err != nil {
		return nil, err
	}
	out := make([]experiment.Event, 0, len(raw))
	for _, e := range raw {
		ev := experiment.Event{ID: e.ID, Channel: e.Channel, Project: e.Project, Query: e.Query, Injected: e.Injected}
		for _, c := range e.Candidates {
			ev.Candidates = append(ev.Candidates, experiment.Scored{ID: c.ID, Score: c.Score})
		}
		out = append(out, ev)
	}
	return out, nil
}

// runExperimentScore reads a labelled sample and reports precision.
func runExperimentScore(ctx context.Context, w io.Writer, args []string) error {
	if len(args) != 1 {
		return errors.New(experimentUsage)
	}
	f, err := os.Open(args[0])
	if err != nil {
		return fmt.Errorf("open labels: %w", err)
	}
	defer f.Close()
	var pairs []experiment.Pair
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var p experiment.Pair
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			return fmt.Errorf("labels line %d: %w", n, err)
		}
		pairs = append(pairs, p)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read labels: %w", err)
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()
	events, err := labelEvents(ctx, d, time.Time{})
	if err != nil {
		return err
	}
	byID := make(map[string]experiment.Event, len(events))
	for _, e := range events {
		byID[e.ID] = e
	}
	renderScores(w, experiment.Score(pairs, byID), len(pairs))
	return nil
}

func renderScores(w io.Writer, s experiment.Scores, total int) {
	fmt.Fprintf(w, "labelled %d of %d pairs, across %d events\n\n", s.Labelled, total, s.Events)
	if s.Labelled == 0 {
		fmt.Fprintln(w, "nothing labelled yet: set \"relevant\" to true or false on each line.")
		return
	}
	prop := func(p experiment.Proportion) string {
		if p.Total == 0 {
			return "-"
		}
		return fmt.Sprintf("%d/%d = %.0f%% (95%% %.0f to %.0f%%)", p.Hits, p.Total, 100*p.Rate, 100*p.Low, 100*p.High)
	}
	channels := make([]string, 0, len(s.Shown))
	for ch := range s.Shown {
		channels = append(channels, ch)
	}
	sort.Strings(channels)
	fmt.Fprintln(w, "precision of what was shown")
	if len(channels) == 0 {
		fmt.Fprintln(w, "  nothing in the sample was shown")
	}
	for _, ch := range channels {
		fmt.Fprintf(w, "  %-12s %s\n", ch, prop(s.Shown[ch]))
	}
	if s.Helpful.Total > 0 {
		fmt.Fprintf(w, "\nprompts with at least one relevant candidate: %s\n", prop(s.Helpful))
		fmt.Fprintln(w, "\nprompt hook replayed at other score floors (top three, both arms)")
		fmt.Fprintf(w, "  %-6s %-40s %s\n", "floor", "precision", "shown per prompt")
		for _, f := range s.Floors {
			fmt.Fprintf(w, "  %-6.1f %-40s %.2f\n", f.Floor, prop(f.Precision), f.PerEvent)
		}
	}
}
