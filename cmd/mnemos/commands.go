package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/installer"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func loadServices(ctx context.Context) (*storage.DB, *memory.Service, *session.Service, *skills.Service, config.Config, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return nil, nil, nil, nil, cfg, fmt.Errorf("load config: %w", err)
	}
	db, err := storage.Open(ctx, cfg.Storage.Path)
	if err != nil {
		return nil, nil, nil, nil, cfg, fmt.Errorf("open storage: %w", err)
	}
	mem := memory.NewService(memory.Config{Store: db.Observations()})
	sess := session.NewService(session.Config{Store: db.Sessions()})
	skl := skills.NewService(skills.Config{Store: db.Skills()})
	return db, mem, sess, skl, cfg, nil
}

func runSearch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mnemos search <query>")
	}
	query := args[0]

	db, mem, _, _, _, err := loadServices(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	results, err := mem.Search(ctx, memory.SearchInput{Query: query, Limit: 20})
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Println("no matches")
		return nil
	}
	for _, r := range results {
		fmt.Printf("  %s  %-8s  %s\n", r.Observation.ID, r.Observation.Type, r.Observation.Title)
		fmt.Printf("    %s\n\n", r.Snippet)
	}
	return nil
}

func runStats(ctx context.Context, _ []string) error {
	db, mem, _, skl, cfg, err := loadServices(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	st, err := mem.Stats(ctx)
	if err != nil {
		return err
	}
	skillCount, _ := skl.List(ctx, "")
	fi, _ := os.Stat(cfg.Storage.Path)
	size := int64(0)
	if fi != nil {
		size = fi.Size()
	}
	fmt.Printf("database:          %s (%d bytes)\n", cfg.Storage.Path, size)
	fmt.Printf("observations:      %d (%d live)\n", st.Observations, st.LiveObservations)
	fmt.Printf("sessions:          %d\n", st.Sessions)
	fmt.Printf("skills:            %d\n", len(skillCount))
	if len(st.TopTags) > 0 {
		fmt.Println("top tags:")
		for _, t := range st.TopTags {
			fmt.Printf("  %-16s %d\n", t.Tag, t.Count)
		}
	}
	return nil
}

func runSessions(ctx context.Context, _ []string) error {
	db, _, sess, _, _, err := loadServices(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	list, err := sess.Recent(ctx, "", 20)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("no sessions yet")
		return nil
	}
	for _, s := range list {
		end := "open"
		if s.EndedAt != nil {
			end = s.EndedAt.Format("2006-01-02 15:04")
		}
		fmt.Printf("  %s  %s..%s  %s  %s\n",
			s.ID, s.StartedAt.Format("2006-01-02 15:04"), end, s.Project, s.Goal)
	}
	return nil
}

func runExport(ctx context.Context, args []string) error {
	db, _, _, _, _, err := loadServices(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	out := io.Writer(os.Stdout)
	if len(args) > 0 {
		f, err := os.Create(args[0])
		if err != nil {
			return fmt.Errorf("create %s: %w", args[0], err)
		}
		defer f.Close()
		out = f
	}

	snapshot, err := dumpAll(ctx, db)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(snapshot)
}

func runImport(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mnemos import <file>")
	}
	_, err := os.Stat(args[0])
	if err != nil {
		return fmt.Errorf("stat %s: %w", args[0], err)
	}
	return fmt.Errorf("import not yet implemented — see issue #1")
}

func runPrune(ctx context.Context, _ []string) error {
	db, mem, _, _, _, err := loadServices(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	n, err := mem.Prune(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("pruned %d expired observations\n", n)
	return nil
}

func runConfig(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	cfgPath := fs.String("path", config.DefaultPath(), "config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

func runInit(_ context.Context, _ []string) error {
	// Determine the absolute path to this binary so the MCP config doesn't
	// rely on $PATH being set correctly for the agent.
	selfPath, err := os.Executable()
	if err != nil {
		selfPath = "mnemos"
	}

	entry := installer.ServerEntry{
		Command: selfPath,
		Args:    []string{"serve"},
	}

	targets := installer.DetectTargets()
	if len(targets) == 0 {
		fmt.Println("no agent clients detected (Claude Code, Cursor, Windsurf).")
		fmt.Println("install one of them first, then run `mnemos init` again.")
		return nil
	}

	for _, t := range targets {
		changed, err := installer.Install(t, entry)
		if err != nil {
			fmt.Printf("  ✗ %s (%s): %v\n", t.Name, t.Path, err)
			continue
		}
		if changed {
			fmt.Printf("  ✓ %s registered at %s\n", t.Name, t.Path)
		} else {
			fmt.Printf("  ○ %s already up to date\n", t.Name)
		}
	}
	fmt.Println()
	fmt.Println("restart your agent. the mnemos_* tools will appear next session.")
	return nil
}

func runDoctor(ctx context.Context, _ []string) error {
	ok := true
	print := func(pass bool, format string, args ...any) {
		prefix := "  ✓ "
		if !pass {
			prefix = "  ✗ "
			ok = false
		}
		fmt.Printf(prefix+format+"\n", args...)
	}

	// Binary and version.
	selfPath, _ := os.Executable()
	print(selfPath != "", "binary path: %s", selfPath)

	// Config + storage.
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		print(false, "config load: %v", err)
		return err
	}
	print(true, "config: %s", config.DefaultPath())

	db, err := storage.Open(ctx, cfg.Storage.Path)
	if err != nil {
		print(false, "storage open: %v", err)
		return err
	}
	defer db.Close()
	st, _ := db.Observations().Stats(ctx)
	print(true, "storage: %s (%d observations)", cfg.Storage.Path, st.Observations)

	// Agent client registrations.
	targets := installer.DetectTargets()
	if len(targets) == 0 {
		print(false, "no agent clients detected")
	}
	for _, t := range targets {
		print(installer.IsInstalled(t), "%s %s", t.Name, t.Path)
	}

	if !ok {
		return errors.New("doctor found issues")
	}
	fmt.Println("all checks passed.")
	return nil
}

type snapshot struct {
	Observations []memory.Observation `json:"observations"`
	Sessions     []session.Session    `json:"sessions"`
	Skills       []skills.Skill       `json:"skills"`
}

func dumpAll(ctx context.Context, db *storage.DB) (*snapshot, error) {
	out := &snapshot{}
	rows, err := db.SQL().QueryContext(ctx, `SELECT id FROM observations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		o, err := db.Observations().Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out.Observations = append(out.Observations, *o)
	}
	if list, err := db.Sessions().Recent(ctx, "", 10000); err == nil {
		out.Sessions = list
	}
	if list, err := db.Skills().List(ctx, ""); err == nil {
		out.Skills = list
	}
	return out, nil
}
