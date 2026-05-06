package main

import (
	"context"
	"fmt"
	"os"

	"github.com/polyxmedia/mnemos/internal/verify"
)

// runVerify dispatches the verify subcommand tree:
//   mnemos verify retrieval [fixture]   — cheap, runs against live store
//   mnemos verify behavior  [fixture]   — expensive, runs claude subprocess
//   mnemos verify all                   — both
func runVerify(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mnemos verify <retrieval|behavior|all> [fixture]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "retrieval":
		return runVerifyRetrieval(ctx, rest)
	case "behavior":
		return runVerifyBehavior(ctx, rest)
	case "all":
		if err := runVerifyRetrieval(ctx, rest); err != nil {
			return err
		}
		fmt.Println()
		return runVerifyBehavior(ctx, rest)
	default:
		return fmt.Errorf("unknown verify subcommand: %s", sub)
	}
}

func runVerifyRetrieval(ctx context.Context, args []string) error {
	path := "verify/retrieval.yaml"
	if len(args) > 0 {
		path = args[0]
	}
	fix, err := verify.LoadRetrievalFixture(path)
	if err != nil {
		return err
	}
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	rep, err := verify.RunRetrieval(ctx, d.mem, fix)
	if err != nil {
		return fmt.Errorf("retrieval: %w", err)
	}
	printRetrievalReport(os.Stdout, rep)
	if rep.Passed < rep.Total {
		return fmt.Errorf("retrieval: %d/%d probes failed", rep.Total-rep.Passed, rep.Total)
	}
	return nil
}

func runVerifyBehavior(ctx context.Context, args []string) error {
	path := "verify/behavior.yaml"
	if len(args) > 0 {
		path = args[0]
	}
	fix, err := verify.LoadBehaviorFixture(path)
	if err != nil {
		return err
	}
	rep, err := verify.RunBehavior(ctx, verify.ExecExecutor{}, fix)
	if err != nil {
		return fmt.Errorf("behavior: %w", err)
	}
	printBehaviorReport(os.Stdout, rep)
	return nil
}

// printRetrievalReport renders one row per probe plus a summary line.
// Failed probes show every query rank so it's obvious whether the target
// was missing entirely (rank 0) or just buried below the threshold.
func printRetrievalReport(w *os.File, rep *verify.RetrievalReport) {
	fmt.Fprintln(w, "retrieval report")
	fmt.Fprintln(w, "================")
	for _, p := range rep.Probes {
		status := "PASS"
		if !p.Pass {
			status = "FAIL"
		}
		title := p.Probe.Title
		if title == "" {
			title = p.Probe.ID
		}
		fmt.Fprintf(w, "  [%s] %s  (best rank %d, want ≤ %d)\n",
			status, title, p.BestRank, p.Probe.ExpectInTop)
		if !p.Pass {
			for _, q := range p.Queries {
				rank := fmt.Sprintf("%d", q.Rank)
				if q.Rank == 0 {
					rank = "miss"
				}
				fmt.Fprintf(w, "      %-6s  %q\n", rank, q.Query)
			}
		}
	}
	fmt.Fprintf(w, "\n  %d/%d probes passed (precision@K)\n",
		rep.Passed, rep.Total)
}

// printBehaviorReport shows on/off pass counts and lift per scenario.
func printBehaviorReport(w *os.File, rep *verify.BehaviorReport) {
	fmt.Fprintln(w, "behavior report")
	fmt.Fprintln(w, "===============")
	fmt.Fprintf(w, "  %-30s  %-7s  %-7s  %s\n", "scenario", "on", "off", "lift")
	for _, s := range rep.Scenarios {
		fmt.Fprintf(w, "  %-30s  %d/%-5d  %d/%-5d  %+.0f%%\n",
			truncate(s.Scenario.Name, 30),
			s.OnPass, s.Runs, s.OffPass, s.Runs, s.Lift()*100)
		if len(s.Errors) > 0 {
			fmt.Fprintf(w, "      %d run errors\n", len(s.Errors))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

