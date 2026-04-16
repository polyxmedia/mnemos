package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/skills"
)

// runSkill handles the `skill` subcommand tree:
//
//	mnemos skill export [names...]          → writes a pack to stdout or --out
//	mnemos skill import <file-or-url>       → imports a pack from disk or HTTP(S)
//	mnemos skill list                       → shows installed skills
func runSkill(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mnemos skill <export|import|list>")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "export":
		return runSkillExport(ctx, rest)
	case "import":
		return runSkillImport(ctx, rest)
	case "list":
		return runSkillList(ctx, rest)
	default:
		return fmt.Errorf("unknown skill subcommand: %s", sub)
	}
}

func runSkillExport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("skill export", flag.ContinueOnError)
	out := fs.String("out", "", "write pack to file (default: stdout)")
	source := fs.String("source", "", "source attribution (e.g. '@voidmode')")
	url := fs.String("url", "", "canonical URL of the pack (optional)")
	project := fs.String("project", "", "project the pack is scoped to (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	pack, err := d.skl.ExportPack(ctx, "", fs.Args(), skills.PackSource{
		Name: *source, URL: *url, Project: *project,
	})
	if err != nil {
		return err
	}
	buf, err := pack.Marshal()
	if err != nil {
		return fmt.Errorf("marshal pack: %w", err)
	}

	if *out == "" {
		_, _ = os.Stdout.Write(buf)
		fmt.Println()
		return nil
	}
	if err := os.WriteFile(*out, buf, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	fmt.Fprintf(os.Stderr, "wrote %d skills to %s\n", len(pack.Skills), *out)
	return nil
}

func runSkillImport(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mnemos skill import <file-or-url>")
	}
	src := args[0]

	reader, closeFn, err := openSource(ctx, src)
	if err != nil {
		return err
	}
	defer closeFn()

	pack, err := skills.UnmarshalPack(reader)
	if err != nil {
		return fmt.Errorf("invalid pack: %w", err)
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	result, err := d.skl.ImportPack(ctx, "", pack)
	if err != nil {
		return err
	}
	attribution := ""
	if pack.Source.Name != "" {
		attribution = " (from " + pack.Source.Name + ")"
	}
	fmt.Printf("imported %d created, %d updated%s\n",
		result.Created, result.Updated, attribution)
	return nil
}

// openSource returns a reader for a skill pack, accepting either a local
// file path or an http(s) URL. The caller must call the returned closeFn.
func openSource(ctx context.Context, src string) (io.Reader, func(), error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("build request: %w", err)
		}
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("fetch %s: %w", src, err)
		}
		if resp.StatusCode/100 != 2 {
			_ = resp.Body.Close()
			return nil, nil, fmt.Errorf("fetch %s: status %d", src, resp.StatusCode)
		}
		return resp.Body, func() { _ = resp.Body.Close() }, nil
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", src, err)
	}
	return f, func() { _ = f.Close() }, nil
}

func runSkillList(ctx context.Context, _ []string) error {
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	list, err := d.skl.List(ctx, "")
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if len(list) == 0 {
		fmt.Println("no skills saved yet")
		return nil
	}
	for _, sk := range list {
		effMark := ""
		if sk.UseCount > 0 {
			effMark = fmt.Sprintf(" · %d uses · %.0f%% effective",
				sk.UseCount, sk.Effectiveness*100)
		}
		fmt.Printf("  %-32s v%d%s\n    %s\n\n", sk.Name, sk.Version, effMark, sk.Description)
	}
	return nil
}
