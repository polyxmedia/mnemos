package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/cue"
	"github.com/polyxmedia/mnemos/internal/memory"
)

const cueUsage = `usage:
  mnemos cue list                          memories with cues
  mnemos cue add <id|handle> <cue>...      attach cues, e.g. "cmd:git commit" or "path:**/migrations/*.sql"
  mnemos cue rm <id|handle> <cue>          detach one cue
  mnemos cue import <file>                 attach cues from lines of "<id|handle> <cue>" (# comments)
  mnemos cue test [--project P] --cmd "<command>" | --path <file>
                                           show which memories would fire`

// runCue manages the cmd: and path: triggers that fire a memory right
// before the matching command or file edit.
func runCue(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(cueUsage)
	}
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	switch args[0] {
	case "list":
		return cueList(ctx, os.Stdout, d)
	case "add":
		if len(args) < 3 {
			return errors.New(cueUsage)
		}
		return cueAdd(ctx, os.Stdout, d, args[1], args[2:])
	case "rm":
		if len(args) != 3 {
			return errors.New(cueUsage)
		}
		return cueRemove(ctx, os.Stdout, d, args[1], args[2])
	case "import":
		if len(args) != 2 {
			return errors.New(cueUsage)
		}
		f, err := os.Open(args[1])
		if err != nil {
			return fmt.Errorf("open cue file: %w", err)
		}
		defer f.Close()
		return cueImport(ctx, os.Stdout, d, f)
	case "test":
		return cueTest(ctx, os.Stdout, d, args[1:])
	}
	return errors.New(cueUsage)
}

func cueList(ctx context.Context, w io.Writer, d *deps) error {
	cued, err := d.db.Cues().Live(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	if len(cued) == 0 {
		fmt.Fprintln(w, "no memories have cues yet")
		return nil
	}
	for _, m := range cued {
		proj := m.Observation.Project
		if proj == "" {
			proj = "global"
		}
		fmt.Fprintf(w, "%s  [%s] %s\n", memory.Handle(m.Observation.ID), proj, m.Observation.Title)
		for _, c := range m.Cues {
			fmt.Fprintf(w, "    %s\n", c)
		}
	}
	return nil
}

func resolveObservation(ctx context.Context, d *deps, ref string) (*memory.Observation, error) {
	o, err := d.mem.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("memory %q: %w", ref, err)
	}
	return o, nil
}

func cueAdd(ctx context.Context, w io.Writer, d *deps, ref string, raw []string) error {
	o, err := resolveObservation(ctx, d, ref)
	if err != nil {
		return err
	}
	cues := make([]string, 0, len(raw))
	for _, r := range raw {
		c, err := cue.Parse(r)
		if err != nil {
			return err
		}
		cues = append(cues, c.String())
	}
	if err := d.db.Cues().Add(ctx, o.ID, cues, time.Now().UTC()); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s %s: %s\n", memory.Handle(o.ID), o.Title, strings.Join(cues, ", "))
	return nil
}

func cueRemove(ctx context.Context, w io.Writer, d *deps, ref, raw string) error {
	o, err := resolveObservation(ctx, d, ref)
	if err != nil {
		return err
	}
	c, err := cue.Parse(raw)
	if err != nil {
		return err
	}
	if err := d.db.Cues().Remove(ctx, o.ID, c.String()); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s: removed %s\n", memory.Handle(o.ID), c)
	return nil
}

// cueImport reads "<id|handle> <cue>" lines. Every line is validated before
// any is written, so a typo on line 9 does not leave lines 1 to 8 applied.
func cueImport(ctx context.Context, w io.Writer, d *deps, r io.Reader) error {
	type pending struct {
		obs *memory.Observation
		cue string
	}
	var todo []pending
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ref, raw, ok := strings.Cut(line, " ")
		if !ok {
			return fmt.Errorf("line %d: want \"<id|handle> <cue>\"", n)
		}
		o, err := resolveObservation(ctx, d, ref)
		if err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
		c, err := cue.Parse(raw)
		if err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
		todo = append(todo, pending{obs: o, cue: c.String()})
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read cue file: %w", err)
	}
	now := time.Now().UTC()
	for _, p := range todo {
		if err := d.db.Cues().Add(ctx, p.obs.ID, []string{p.cue}, now); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s %s: %s\n", memory.Handle(p.obs.ID), p.obs.Title, p.cue)
	}
	return nil
}

// cueTest shows what would fire, ignoring the repeat window, so a cue can
// be checked before it is relied on.
func cueTest(ctx context.Context, w io.Writer, d *deps, args []string) error {
	fs := flag.NewFlagSet("cue test", flag.ContinueOnError)
	project := fs.String("project", "", "project the action runs in (default: the git repo of cwd)")
	command := fs.String("cmd", "", "shell command to test")
	path := fs.String("path", "", "file path to test, relative to the project root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*command == "") == (*path == "") {
		return errors.New(cueUsage)
	}
	proj := *project
	if proj == "" {
		wd, _ := os.Getwd()
		proj = projectFromCWD(wd)
	}
	cued, err := d.db.Cues().Live(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	fired := 0
	for _, m := range cued {
		if m.Observation.Project != "" && m.Observation.Project != proj {
			continue
		}
		for _, raw := range m.Cues {
			c, err := cue.Parse(raw)
			if err != nil {
				continue
			}
			if (*command != "" && c.MatchCommand(*command)) || (*path != "" && c.MatchPath(*path)) {
				fmt.Fprintf(w, "fires  %s  %s  (cue %s)\n", memory.Handle(m.Observation.ID), m.Observation.Title, c)
				fired++
				break
			}
		}
	}
	if fired == 0 {
		fmt.Fprintf(w, "nothing fires in project %s\n", proj)
	}
	return nil
}
