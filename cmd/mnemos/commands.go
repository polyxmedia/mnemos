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
	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/installer"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// deps bundles the services a CLI subcommand typically needs. Returned by
// loadDeps so callers take exactly what they use.
type deps struct {
	db   *storage.DB
	mem  *memory.Service
	sess *session.Service
	skl  *skills.Service
	rum  *rumination.Service
	cfg  config.Config
}

func (d *deps) close() { _ = d.db.Close() }

func loadDeps(ctx context.Context) (*deps, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	db, err := storage.Open(ctx, cfg.Storage.Path)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}
	skl := skills.NewService(skills.Config{Store: db.Skills()})
	return &deps{
		db: db,
		mem: memory.NewService(memory.Config{
			Store:      db.Observations(),
			Injections: injection.NewLogger(db.Injections(), nil),
		}),
		sess: session.NewService(session.Config{Store: db.Sessions()}),
		skl:  skl,
		rum:  newRumination(cfg, db, skl),
		cfg:  cfg,
	}, nil
}

// newRumination composes the rumination service with whatever monitors
// the config enables. When `[rumination].enabled = false` we return nil
// so callers (dream, MCP surface) quietly skip the feature — same guarded
// pattern as skill promotion.
func newRumination(cfg config.Config, db *storage.DB, skl *skills.Service) *rumination.Service {
	if !cfg.Rumination.Enabled {
		return nil
	}
	monitors := []rumination.Monitor{
		&rumination.SkillEffectivenessMonitor{
			Skills:  skl,
			Floor:   cfg.Rumination.SkillEffectivenessFloor,
			MinUses: cfg.Rumination.SkillMinUses,
		},
		&rumination.StaleSkillMonitor{
			Skills:           skl,
			StaleDays:        cfg.Rumination.StaleSkillDays,
			MaxEffectiveness: cfg.Rumination.StaleSkillFloor,
		},
		&rumination.CorrectionRepeatUnderSkillMonitor{
			Corrections:     db.Observations(),
			Skills:          skl,
			RepeatThreshold: cfg.Rumination.CorrectionRepeatN,
		},
		&rumination.ContradictionDetectedMonitor{
			Links:     db.Observations(),
			Threshold: cfg.Rumination.ContradictionThreshold,
		},
		&rumination.StaleRawObservationMonitor{
			Observations: db.Observations(),
			StaleDays:    cfg.Rumination.StaleRawDays,
		},
	}
	return rumination.NewService(rumination.Config{
		Monitors: monitors,
		Skills:   skl,
		Memory:   db.Observations(),
		Store:    db.Rumination(),
	})
}

func runSearch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mnemos search <query>")
	}
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	results, mode, err := d.mem.SearchWithMode(ctx, memory.SearchInput{Query: args[0], Limit: 20})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if len(results) == 0 {
		fmt.Println("no matches")
		return nil
	}
	for _, r := range results {
		fmt.Printf("  %s  %-8s  %s\n", r.Observation.ID, r.Observation.Type, r.Observation.Title)
		fmt.Printf("    %s\n\n", r.Snippet)
	}
	fmt.Printf("%d result(s) · retrieval: %s\n", len(results), mode)
	return nil
}

func runStats(ctx context.Context, _ []string) error {
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	st, err := d.mem.Stats(ctx)
	if err != nil {
		return fmt.Errorf("stats: %w", err)
	}
	skillList, _ := d.skl.List(ctx, "")
	promotedCount := 0
	for _, s := range skillList {
		for _, tag := range s.Tags {
			if tag == "auto-promoted" {
				promotedCount++
				break
			}
		}
	}
	fi, _ := os.Stat(d.cfg.Storage.Path)
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	fmt.Printf("database:          %s (%d bytes)\n", d.cfg.Storage.Path, size)
	fmt.Printf("observations:      %d (%d live)\n", st.Observations, st.LiveObservations)
	fmt.Printf("sessions:          %d\n", st.Sessions)
	fmt.Printf("skills:            %d (%d auto-promoted from corrections)\n",
		len(skillList), promotedCount)
	if d.rum != nil {
		if c, err := d.rum.Counts(ctx); err == nil && (c.Pending+c.Resolved+c.Dismissed) > 0 {
			fmt.Printf("rumination:        %d pending, %d resolved, %d dismissed\n",
				c.Pending, c.Resolved, c.Dismissed)
		}
	}
	if len(st.TopTags) > 0 {
		fmt.Println("top tags:")
		for _, t := range st.TopTags {
			fmt.Printf("  %-16s %d\n", t.Tag, t.Count)
		}
	}
	return nil
}

func runSessions(ctx context.Context, _ []string) error {
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	list, err := d.sess.Recent(ctx, "", 20)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
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
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	out := io.Writer(os.Stdout)
	if len(args) > 0 {
		f, err := os.Create(args[0])
		if err != nil {
			return fmt.Errorf("create %s: %w", args[0], err)
		}
		defer f.Close()
		out = f
	}

	snap, err := dumpAll(ctx, d.db)
	if err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	return nil
}

func runImport(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mnemos import <file>")
	}
	f, err := os.Open(args[0])
	if err != nil {
		return fmt.Errorf("open %s: %w", args[0], err)
	}
	defer f.Close()

	var snap snapshot
	if err := json.NewDecoder(f).Decode(&snap); err != nil {
		return fmt.Errorf("decode %s: %w", args[0], err)
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	// Fidelity restore: every record keeps its original ID, timestamps,
	// provenance, and (for observations) bi-temporal validity. Re-importing a
	// record already present is skipped, not duplicated or re-versioned. This
	// is a trusted operation, the JSON equivalent of restoring a database
	// backup, so it preserves trust tiers verbatim rather than quarantining.
	//
	// Order matters for the observation -> session foreign key: sessions
	// restore first (with their original IDs), then observations resolve.
	var (
		obsAdded, obsSkipped     int
		sessAdded, sessSkipped   int
		skillAdded, skillSkipped int
		repointed                int
		knownSessions            = make(map[string]bool, len(snap.Sessions))
	)
	for i := range snap.Sessions {
		s := snap.Sessions[i]
		knownSessions[s.ID] = true
		added, err := d.sess.Restore(ctx, &s)
		if err != nil {
			return fmt.Errorf("import session %s: %w", s.ID, err)
		}
		countAddSkip(added, &sessAdded, &sessSkipped)
	}
	for i := range snap.Skills {
		sk := snap.Skills[i]
		added, err := d.skl.Restore(ctx, &sk)
		if err != nil {
			return fmt.Errorf("import skill %s: %w", sk.Name, err)
		}
		countAddSkip(added, &skillAdded, &skillSkipped)
	}
	for i := range snap.Observations {
		o := snap.Observations[i]
		// Drop a session link the dump does not carry, so the foreign key
		// cannot dangle. Rare in a full dump; happens with edited or partial
		// exports.
		if o.SessionID != "" && !knownSessions[o.SessionID] {
			o.SessionID = ""
			repointed++
		}
		added, err := d.mem.Restore(ctx, &o)
		if err != nil {
			return fmt.Errorf("import observation %s: %w", o.ID, err)
		}
		countAddSkip(added, &obsAdded, &obsSkipped)
	}
	fmt.Printf("imported from %s: observations %d added / %d skipped, sessions %d / %d, skills %d / %d\n",
		args[0], obsAdded, obsSkipped, sessAdded, sessSkipped, skillAdded, skillSkipped)
	if repointed > 0 {
		fmt.Printf("  %d observation(s) had a dangling session link cleared\n", repointed)
	}
	return nil
}

func countAddSkip(added bool, addCount, skipCount *int) {
	if added {
		*addCount++
	} else {
		*skipCount++
	}
}

func runPrune(ctx context.Context, _ []string) error {
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	n, err := d.mem.Prune(ctx)
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}
	fmt.Printf("pruned %d expired observations\n", n)
	return nil
}

func runConfig(_ context.Context, args []string) error {
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
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return nil
}

func runInit(_ context.Context, _ []string) error {
	selfPath, err := os.Executable()
	if err != nil {
		selfPath = "mnemos"
	}
	entry := installer.ServerEntry{Command: selfPath, Args: []string{"serve"}}

	targets := installer.DetectTargets()
	if len(targets) == 0 {
		fmt.Println("no agent clients detected (Claude Code, Claude Desktop, Cursor, Windsurf, Codex CLI).")
		fmt.Println("install one of them first, then run `mnemos init` again.")
		return nil
	}
	hasClaudeCode := false
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
		if t.Name == "Claude Code (user)" {
			hasClaudeCode = true
		}
	}

	// Claude Code specific hooks. Without them, mnemos tool calls are
	// optional to the agent, and agents skip optional calls when the task
	// looks like plain editing — the failure this wiring exists to fix.
	if hasClaudeCode {
		settings := installer.ClaudeSettingsPath()
		hookEntries := []struct {
			label string
			entry installer.HookEntry
		}{
			{
				label: "Claude Code SessionStart hook",
				entry: installer.HookEntry{
					Event:   "SessionStart",
					Matcher: "startup",
					Command: fmt.Sprintf("%s prewarm", selfPath),
					Timeout: 10,
				},
			},
			{
				label: "Claude Code UserPromptSubmit hook",
				entry: installer.HookEntry{
					Event:   "UserPromptSubmit",
					Command: fmt.Sprintf("%s hook user-prompt", selfPath),
					Timeout: 5,
				},
			},
			{
				label: "Claude Code PostToolUse hook",
				entry: installer.HookEntry{
					Event:   "PostToolUse",
					Matcher: "Edit|Write|MultiEdit|NotebookEdit",
					Command: fmt.Sprintf("%s hook post-tool", selfPath),
					Timeout: 5,
				},
			},
			{
				label: "Claude Code SessionEnd hook",
				entry: installer.HookEntry{
					Event:   "SessionEnd",
					Command: fmt.Sprintf("%s hook session-end", selfPath),
					Timeout: 5,
				},
			},
			{
				label: "Claude Code PreToolUse guardrail",
				entry: installer.HookEntry{
					Event:   "PreToolUse",
					Matcher: "mcp__mnemos__mnemos_save|mcp__mnemos__mnemos_correct|mcp__mnemos__mnemos_convention",
					Command: fmt.Sprintf("%s hook pre-tool", selfPath),
					Timeout: 5,
				},
			},
			{
				label: "Claude Code PreToolUse just-in-time memory",
				entry: installer.HookEntry{
					Event:   "PreToolUse",
					Matcher: "Edit|Write|MultiEdit|NotebookEdit",
					Command: fmt.Sprintf("%s hook pre-tool", selfPath),
					Timeout: 5,
				},
			},
			{
				label: "Claude Code PreCompact hook",
				entry: installer.HookEntry{
					Event:   "PreCompact",
					Command: fmt.Sprintf("%s hook pre-compact", selfPath),
					Timeout: 10,
				},
			},
			{
				label: "Claude Code PostCompact hook",
				entry: installer.HookEntry{
					Event:   "PostCompact",
					Command: fmt.Sprintf("%s hook post-compact", selfPath),
					Timeout: 10,
				},
			},
		}
		for _, h := range hookEntries {
			changed, err := installer.InstallHook(settings, h.entry)
			switch {
			case err != nil:
				fmt.Printf("  ✗ %s (%s): %v\n", h.label, settings, err)
			case changed:
				fmt.Printf("  ✓ %s wired at %s\n", h.label, settings)
			default:
				fmt.Printf("  ○ %s already up to date\n", h.label)
			}
		}
	}

	fmt.Println()
	fmt.Println("restart your agent. the mnemos_* tools will appear next session.")
	return nil
}

func runDoctor(ctx context.Context, _ []string) error {
	ok := true
	check := func(pass bool, format string, args ...any) {
		prefix := "  ✓ "
		if !pass {
			prefix = "  ✗ "
			ok = false
		}
		fmt.Printf(prefix+format+"\n", args...)
	}

	selfPath, _ := os.Executable()
	check(selfPath != "", "binary path: %s", selfPath)

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		check(false, "config load: %v", err)
		return fmt.Errorf("doctor: %w", err)
	}
	check(true, "config: %s", config.DefaultPath())

	db, err := storage.Open(ctx, cfg.Storage.Path)
	if err != nil {
		check(false, "storage open: %v", err)
		return fmt.Errorf("doctor: %w", err)
	}
	defer db.Close()
	st, _ := db.Observations().Stats(ctx)
	check(true, "storage: %s (%d observations)", cfg.Storage.Path, st.Observations)

	targets := installer.DetectTargets()
	if len(targets) == 0 {
		check(false, "no agent clients detected")
	}
	hasClaudeCode := false
	for _, t := range targets {
		check(installer.IsInstalled(t), "%s %s", t.Name, t.Path)
		if t.Name == "Claude Code (user)" {
			hasClaudeCode = true
		}
	}

	if hasClaudeCode {
		settings := installer.ClaudeSettingsPath()
		doctorHooks := []struct {
			label string
			entry installer.HookEntry
		}{
			{"Claude Code SessionStart hook", installer.HookEntry{
				Event:   "SessionStart",
				Matcher: "startup",
				Command: fmt.Sprintf("%s prewarm", selfPath),
			}},
			{"Claude Code UserPromptSubmit hook", installer.HookEntry{
				Event:   "UserPromptSubmit",
				Command: fmt.Sprintf("%s hook user-prompt", selfPath),
			}},
			{"Claude Code PostToolUse hook", installer.HookEntry{
				Event:   "PostToolUse",
				Matcher: "Edit|Write|MultiEdit|NotebookEdit",
				Command: fmt.Sprintf("%s hook post-tool", selfPath),
			}},
			{"Claude Code SessionEnd hook", installer.HookEntry{
				Event:   "SessionEnd",
				Command: fmt.Sprintf("%s hook session-end", selfPath),
			}},
			{"Claude Code PreToolUse guardrail", installer.HookEntry{
				Event:   "PreToolUse",
				Matcher: "mcp__mnemos__mnemos_save|mcp__mnemos__mnemos_correct|mcp__mnemos__mnemos_convention",
				Command: fmt.Sprintf("%s hook pre-tool", selfPath),
			}},
			{"Claude Code PreCompact hook", installer.HookEntry{
				Event:   "PreCompact",
				Command: fmt.Sprintf("%s hook pre-compact", selfPath),
			}},
			{"Claude Code PostCompact hook", installer.HookEntry{
				Event:   "PostCompact",
				Command: fmt.Sprintf("%s hook post-compact", selfPath),
			}},
		}
		for _, h := range doctorHooks {
			check(installer.IsHookInstalled(settings, h.entry), "%s %s", h.label, settings)
		}
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
	// Single drained query, no per-id Get. The previous loop held a SELECT
	// cursor open while calling Get (which itself queries and writes
	// access_count) for each row, which deadlocks the single-connection pool
	// on any populated store and bumps access counts as a side effect of
	// merely exporting.
	obs, err := db.Observations().Export(ctx)
	if err != nil {
		return nil, fmt.Errorf("export observations: %w", err)
	}
	out.Observations = obs
	if list, err := db.Sessions().Recent(ctx, "", 10000); err == nil {
		out.Sessions = list
	}
	if list, err := db.Skills().List(ctx, ""); err == nil {
		out.Skills = list
	}
	return out, nil
}
