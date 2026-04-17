package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/polyxmedia/mnemos/internal/replay"
)

// runReplay implements `mnemos replay <session_id>`. Outputs a markdown
// block combining the session's original work with everything learned
// since — designed to be pasted back into an agent's context.
func runReplay(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	out := fs.String("out", "", "write replay to file (default: stdout)")
	maxTokens := fs.Int("max-tokens", 0, "override the token budget (default 3000)")
	project := fs.String("project", "", "override project (default: session's project)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: mnemos replay <session_id>")
	}
	sessionID := fs.Arg(0)

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	svc := replay.NewService(replay.Config{
		Observations: d.db.Observations(),
		Sessions:     d.db.Sessions(),
		Skills:       d.db.Skills(),
		MaxTokens:    *maxTokens,
	})
	block, err := svc.Build(ctx, replay.Request{
		SessionID: sessionID, Project: *project, MaxTokens: *maxTokens,
	})
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}

	if *out != "" {
		if err := os.WriteFile(*out, []byte(block.Text), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", *out, err)
		}
		fmt.Fprintf(os.Stderr, "replay (~%d tokens) written to %s\n", block.TokenEstimate, *out)
		return nil
	}
	fmt.Print(block.Text)
	return nil
}
