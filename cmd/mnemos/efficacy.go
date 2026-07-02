package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/polyxmedia/mnemos/internal/storage"
)

// runEfficacy prints whether surfacing memory goes with sessions that end
// well. Where `digest` counts activity, `efficacy` correlates that activity
// with outcomes — the closest read on "is mnemos earning its keep" the
// store can produce from the surfaced-vs-outcome data it already logs.
func runEfficacy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("efficacy", flag.ContinueOnError)
	since := fs.Duration("since", 30*24*time.Hour, "look-back window (Go duration: 24h, 168h, 720h)")
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

	stats, err := d.db.Efficacy(ctx, time.Now().UTC().Add(-*since))
	if err != nil {
		return err
	}

	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(stats)
	}
	renderEfficacy(os.Stdout, stats, *since)
	return nil
}

// renderEfficacy writes the human-readable efficacy report. Pure function
// of its inputs so tests can assert the exact shape.
func renderEfficacy(w io.Writer, s *storage.EfficacyStats, window time.Duration) {
	fmt.Fprintf(w, "mnemos efficacy — last %s\n\n", formatWindow(window))

	total := s.WithInjections.Sessions + s.WithoutInjections.Sessions
	if total == 0 {
		fmt.Fprintln(w, "no sessions ended in this window — nothing to attribute")
		return
	}

	fmt.Fprintln(w, "session outcomes by whether memory was surfaced:")
	fmt.Fprintf(w, "  with injections:    %3d sessions, %d ok (%.0f%%)\n",
		s.WithInjections.Sessions, s.WithInjections.OK, s.WithInjections.OKPct())
	fmt.Fprintf(w, "  without injections: %3d sessions, %d ok (%.0f%%)\n",
		s.WithoutInjections.Sessions, s.WithoutInjections.OK, s.WithoutInjections.OKPct())
	if s.WithInjections.Sessions > 0 && s.WithoutInjections.Sessions > 0 {
		lift := s.WithInjections.OKPct() - s.WithoutInjections.OKPct()
		fmt.Fprintf(w, "  gap: %+.0f percentage points\n", lift)
	}

	if len(s.ByChannel) > 0 {
		fmt.Fprintln(w, "\nby channel (ok-rate of sessions each channel surfaced into):")
		for _, c := range s.ByChannel {
			fmt.Fprintf(w, "  %-12s %5d surfacings across %3d sessions — %2d ok (%.0f%%)\n",
				c.Channel, c.Surfacings, c.Sessions, c.OKSessions, c.OKPct())
		}
	}

	if len(s.ByMemory) > 0 {
		fmt.Fprintln(w, "\nmost-surfaced memories and their session outcomes:")
		for _, m := range s.ByMemory {
			title := m.Title
			if title == "" {
				title = m.RefID
			}
			fmt.Fprintf(w, "  [%s] %s\n      %d surfacings across %d sessions — %d ok (%.0f%%)\n",
				m.Kind, title, m.Surfacings, m.Sessions, m.OKSessions, m.OKPct())
		}
	}

	// Honesty caveat: this is correlation, not proof. A session only carries
	// injections when mnemos_session_start ran, which itself signals a more
	// disciplined session. There is no explicit "this memory was applied"
	// signal yet, so the numbers show association, not causation.
	fmt.Fprintln(w, "\nnote: correlational, not causal — sessions with injections are ones where")
	fmt.Fprintln(w, "mnemos_session_start ran, which itself correlates with disciplined sessions.")
	fmt.Fprintln(w, "no explicit applied-signal is logged yet, so read these as association.")
}
