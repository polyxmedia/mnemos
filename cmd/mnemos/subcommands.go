package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/polyxmedia/mnemos/internal/dream"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/vault"
)

// runDream executes one dream-cycle consolidation pass and prints the
// journal. Non-blocking — safe to run while `mnemos serve` is active
// because SQLite+WAL serialises writes.
func runDream(ctx context.Context, _ []string) error {
	db, mem, _, _, _, err := loadServices(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	svc := dream.NewService(dream.Config{
		Memory: mem,
		Store:  db.Observations(),
		Logger: slog.Default(),
	})
	j, err := svc.Run(ctx, true)
	if err != nil {
		return err
	}
	fmt.Println(j.Summary())
	return nil
}

// runVault handles the `vault` subcommand tree: export | status.
func runVault(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mnemos vault <export|status>")
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "export":
		return runVaultExport(ctx, rest)
	case "status":
		return runVaultStatus(ctx, rest)
	default:
		return fmt.Errorf("unknown vault subcommand: %s", sub)
	}
}

func runVaultExport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("vault export", flag.ContinueOnError)
	out := fs.String("out", "", "override vault output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, _, _, _, cfg, err := loadServices(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	root := cfg.Vault.Path
	if *out != "" {
		root = *out
	}
	if root == "" {
		return fmt.Errorf("no vault path configured; pass --out or set [vault].path")
	}

	ex := vault.NewExporter(vault.Config{
		Root:     root,
		Obs:      db.Observations(),
		Sessions: db.Sessions(),
		Skills:   db.Skills(),
	})
	stats, err := ex.ExportAll(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("exported %d observations, %d sessions, %d skills, %d tag MOCs to %s\n",
		stats.Observations, stats.Sessions, stats.Skills, stats.Tags, root)
	return nil
}

func runVaultStatus(ctx context.Context, _ []string) error {
	db, _, _, _, cfg, err := loadServices(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Printf("vault path: %s\n", cfg.Vault.Path)
	if _, err := os.Stat(cfg.Vault.Path); err != nil {
		fmt.Println("status:     not yet exported")
		return nil
	}
	fmt.Println("status:     exists")
	return nil
}

// runEmbed handles the `embed` subcommand tree: status | backfill.
func runEmbed(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mnemos embed <status|backfill>")
	}
	sub := args[0]
	switch sub {
	case "status":
		return runEmbedStatus(ctx)
	case "backfill":
		return runEmbedBackfill(ctx)
	default:
		return fmt.Errorf("unknown embed subcommand: %s", sub)
	}
}

func runEmbedStatus(ctx context.Context) error {
	_, mem, _, _, cfg, err := loadServices(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("provider: %s\n", cfg.Embedding.Provider)
	fmt.Printf("model:    %s\n", cfg.Embedding.Model)
	fmt.Printf("enabled:  %v\n", mem.HybridEnabled())
	return nil
}

func runEmbedBackfill(ctx context.Context) error {
	db, _, _, _, cfg, err := loadServices(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	embedder := selectEmbedder(ctx, cfg.Embedding)
	if embedder.Dimension() == 0 {
		return fmt.Errorf("no embedding provider available — check `embed status`")
	}

	store := db.Observations()
	var total int
	for {
		batch, err := store.ListMissingEmbeddings(ctx, 100)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, o := range batch {
			text := o.Title + "\n" + o.Content
			if o.Rationale != "" {
				text += "\n" + o.Rationale
			}
			vec, err := embedder.Embed(ctx, text)
			if err != nil {
				slog.Warn("embed failed", "id", o.ID, "err", err)
				continue
			}
			if err := store.UpdateEmbedding(ctx, o.ID, embedder.Model(), vec); err != nil {
				return err
			}
			total++
		}
		if len(batch) < 100 {
			break
		}
	}
	fmt.Printf("backfilled %d embeddings\n", total)
	return nil
}

// Silence unused-variable lint for the memory.Observation type import.
var _ = memory.Observation{}
