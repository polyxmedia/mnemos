package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/storage"
)

// runDigest prints an activity summary for the last N hours: what was
// saved, what was surfaced, which sessions ran, whether the dream pass
// fired. The "numbers on the screen" view of the store earning its keep.
func runDigest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("digest", flag.ContinueOnError)
	since := fs.Duration("since", 24*time.Hour, "look-back window (Go duration: 24h, 168h, 30m)")
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

	stats, err := d.db.Digest(ctx, time.Now().UTC().Add(-*since))
	if err != nil {
		return err
	}

	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(stats)
	}
	renderDigest(os.Stdout, stats, *since)
	return nil
}

// renderDigest writes the human-readable digest. Pure function of its
// inputs so tests can assert the exact shape.
func renderDigest(w io.Writer, s *storage.DigestStats, window time.Duration) {
	fmt.Fprintf(w, "mnemos digest — last %s\n\n", formatWindow(window))

	if s.ObservationsSaved == 0 && s.SessionsOpened == 0 && s.InjectionsTotal == 0 && s.SkillsTouched == 0 {
		fmt.Fprintln(w, "no activity recorded in this window")
		return
	}

	fmt.Fprintf(w, "observations saved: %d%s\n", s.ObservationsSaved, kindSuffix(s.ObservationsByType))
	fmt.Fprintf(w, "sessions: %d opened, %d closed%s\n", s.SessionsOpened, s.SessionsClosed, kindSuffix(s.SessionsByStatus))
	fmt.Fprintf(w, "skills created or updated: %d\n", s.SkillsTouched)
	fmt.Fprintf(w, "memories surfaced into context: %d%s\n", s.InjectionsTotal, kindSuffix(s.InjectionsByChannel))
	if len(s.TopSurfaced) > 0 {
		fmt.Fprintln(w, "most surfaced:")
		for _, t := range s.TopSurfaced {
			title := t.Title
			if title == "" {
				title = t.RefID
			}
			fmt.Fprintf(w, "  - [%s] %s (%d×)\n", t.Kind, title, t.Count)
		}
	}
	if s.DreamPasses > 0 {
		fmt.Fprintf(w, "dream passes: %d\n", s.DreamPasses)
	}
}

// kindSuffix renders a (label: count) breakdown as " (a: 1, b: 2)", or ""
// when there is nothing to break down.
func kindSuffix(counts []storage.KindCount) string {
	if len(counts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(counts))
	for _, kc := range counts {
		parts = append(parts, fmt.Sprintf("%s: %d", kc.Kind, kc.Count))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// formatWindow renders a duration without the noisy zero components Go's
// String produces ("24h0m0s" → "24h", "30m0s" → "30m").
func formatWindow(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	default:
		return d.String()
	}
}
