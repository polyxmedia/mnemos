// Mnemos CLI. Subcommands are thin wrappers over the domain services.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/polyxmedia/mnemos/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()

	cmd := os.Args[1]
	args := os.Args[2:]

	handler, ok := commands[cmd]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage(os.Stderr)
		os.Exit(2)
	}

	if err := handler(ctx, args); err != nil {
		fmt.Fprintf(os.Stderr, "mnemos: %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

type handler func(ctx context.Context, args []string) error

var commands = map[string]handler{
	"serve":    runServe,
	"search":   runSearch,
	"stats":    runStats,
	"sessions": runSessions,
	"export":   runExport,
	"import":   runImport,
	"prune":    runPrune,
	"dream":    runDream,
	"vault":    runVault,
	"embed":    runEmbed,
	"version":  runVersion,
	"config":   runConfig,
	"init":     runInit,
	"doctor":   runDoctor,
	"-v":       runVersion,
	"--version": runVersion,
	"-h":       runHelp,
	"--help":   runHelp,
	"help":     runHelp,
}

func runVersion(_ context.Context, _ []string) error {
	fmt.Println("mnemos", version.Version)
	return nil
}

func runHelp(_ context.Context, _ []string) error {
	printUsage(os.Stdout)
	return nil
}

func printUsage(w *os.File) {
	fmt.Fprintf(w, `mnemos — persistent memory and skills for AI coding agents

Usage:
  mnemos <command> [flags]

Commands:
  serve [--http ADDR]    Start the MCP stdio server (or HTTP if --http given)
  search <query>         Search observations from the terminal
  stats                  Show memory statistics
  sessions               List recent sessions
  export [file]          Export all data as JSON
  import <file>          Import data from JSON
  prune                  Remove expired observations
  dream                  Run one consolidation pass (dedup, decay, prune)
  vault export           Export memory to an Obsidian-compatible vault
  vault status           Show vault sync status
  embed status           Show embedding provider status
  embed backfill         Generate embeddings for observations that lack them
  init                   Register mnemos with Claude Code / Cursor / Windsurf
  doctor                 Check installation health
  config                 Print the current configuration
  version                Print the binary version

First run:
  mnemos init            # auto-wires to Claude Code, Cursor, Windsurf
  Restart your agent.    # new tools will appear under mnemos_*
`)
}
