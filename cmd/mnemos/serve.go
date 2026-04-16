package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/polyxmedia/mnemos/internal/api"
	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/mcp"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/internal/version"
)

// runServe starts the MCP stdio server (default) or the HTTP server when
// --http is passed. Logs go to stderr so they don't pollute the stdio
// JSON-RPC stream on stdout.
func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	httpAddr := fs.String("http", "", "start HTTP server on ADDR instead of stdio (e.g. :8080)")
	cfgPath := fs.String("config", config.DefaultPath(), "path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	db, err := storage.Open(ctx, cfg.Storage.Path)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer db.Close()

	embedder := selectEmbedder(ctx, cfg.Embedding)
	logger.Info("embedding", "provider", embedder.Model(), "dim", embedder.Dimension())

	mem := memory.NewService(memory.Config{
		Store:    db.Observations(),
		Embedder: embedder,
		RankParams: memory.RankParams{
			DecayRate:        cfg.Search.DecayRate,
			ImportanceWeight: 0.5,
			AccessBoost:      0.1,
		},
		Hybrid: memory.HybridParams{Alpha: cfg.Search.HybridAlpha, K: 60},
	})
	sess := session.NewService(session.Config{Store: db.Sessions()})
	skl := skills.NewService(skills.Config{Store: db.Skills()})
	pw := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(),
		Sessions:     db.Sessions(),
		Skills:       db.Skills(),
		Touches:      db.Touches(),
		MaxTokens:    500,
	})

	addr := *httpAddr
	if addr != "" || cfg.Server.Transport == "http" {
		if addr == "" {
			addr = cfg.Server.HTTPAddr
		}
		httpSrv := api.NewServer(api.Config{
			Memory:   mem,
			Sessions: sess,
			Skills:   skl,
			Touches:  db.Touches(),
			Prewarm:  pw,
			APIKey:   cfg.Server.APIKey,
			Logger:   logger,
		})
		logger.Info("mnemos serve (http)", "version", version.Version, "addr", addr)
		return httpSrv.Serve(ctx, addr)
	}

	srv := mcp.NewServer(mcp.Config{
		Name:     "mnemos",
		Version:  version.Version,
		Memory:   mem,
		Sessions: sess,
		Skills:   skl,
		Touches:  db.Touches(),
		Prewarm:  pw,
		Logger:   logger,
	})

	logger.Info("mnemos serve (stdio)", "version", version.Version, "db", cfg.Storage.Path)
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
