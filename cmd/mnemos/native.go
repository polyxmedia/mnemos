package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/polyxmedia/mnemos/internal/native"
)

// runNative inspects the read-only index of Claude Code's auto memory notes.
func runNative(ctx context.Context, args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	if sub != "status" && sub != "sync" {
		return errors.New("usage: mnemos native [status|sync]")
	}
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	if d.native == nil {
		fmt.Println("native memory index is off. Enable it in the config:")
		fmt.Println()
		fmt.Println("  [native_memory]")
		fmt.Println("  enabled = true")
		return nil
	}
	stats, err := d.native.Sync(ctx)
	if err != nil {
		return fmt.Errorf("native sync: %w", err)
	}
	notes, repos, err := d.native.Counts(ctx)
	if err != nil {
		return fmt.Errorf("native counts: %w", err)
	}
	fmt.Printf("roots: %s\n", strings.Join(d.native.Roots(), ", "))
	fmt.Printf("indexed: %d notes across %d repos\n", notes, repos)
	fmt.Printf("this scan: %d files seen, %d updated, %d removed\n", stats.Scanned, stats.Updated, stats.Removed)
	return nil
}

// searchNotes returns note hits for the search command, or nil when the index
// is off or the lookup fails.
func searchNotes(ctx context.Context, d *deps, query string) []native.Hit {
	if d.native == nil {
		return nil
	}
	hits, err := d.native.Search(ctx, query, 5)
	if err != nil {
		return nil
	}
	return hits
}

func printNoteHits(hits []native.Hit) {
	if len(hits) == 0 {
		return
	}
	fmt.Println("auto memory notes (read-only, from any repo):")
	for _, h := range hits {
		fmt.Printf("  %s  %s  %s\n", h.ID, h.Project, h.Name)
		if h.Description != "" {
			fmt.Printf("    %s\n", h.Description)
		}
		if len(h.AlsoIn) > 0 {
			fmt.Printf("    also in %d other repo(s)\n", len(h.AlsoIn))
		}
		fmt.Println()
	}
}
