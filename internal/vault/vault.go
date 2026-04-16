// Package vault exports Mnemos content as an Obsidian-compatible vault:
// one markdown file per observation/session/skill with YAML frontmatter
// and wikilinks. Users get a browsable, human-readable view of their
// agent's memory — a genuine second brain interface, not a debug tool.
package vault

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// Exporter writes a vault directory structure:
//
//	{root}/
//	  observations/{yyyy-mm-dd}-{slug}.md
//	  sessions/{yyyy-mm-dd}-{slug}.md
//	  skills/{slug}.md
//	  tags/{tag}.md           # MOC (Map of Content)
//	  _index.md               # dashboard
type Exporter struct {
	root    string
	obs     memory.Store
	sess    session.Store
	skill   skills.Store
	log     *slog.Logger
}

// Config bundles dependencies.
type Config struct {
	Root     string
	Obs      memory.Store
	Sessions session.Store
	Skills   skills.Store
	Logger   *slog.Logger
}

// NewExporter constructs an Exporter.
func NewExporter(cfg Config) *Exporter {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Exporter{
		root:  cfg.Root,
		obs:   cfg.Obs,
		sess:  cfg.Sessions,
		skill: cfg.Skills,
		log:   cfg.Logger,
	}
}

// ExportAll writes the full vault. Overwrites existing files. Not
// incremental — use Sync for watch-mode.
type ExportStats struct {
	Observations int
	Sessions     int
	Skills       int
	Tags         int
}

// ExportAll walks every store and writes the full vault structure.
func (e *Exporter) ExportAll(ctx context.Context) (ExportStats, error) {
	var stats ExportStats
	if err := ensureDir(filepath.Join(e.root, "observations")); err != nil {
		return stats, err
	}
	if err := ensureDir(filepath.Join(e.root, "sessions")); err != nil {
		return stats, err
	}
	if err := ensureDir(filepath.Join(e.root, "skills")); err != nil {
		return stats, err
	}
	if err := ensureDir(filepath.Join(e.root, "tags")); err != nil {
		return stats, err
	}

	// Observations: pull everything (no bulk-list method exists; use a
	// simple project-agnostic list via an empty-filter ListByProject with
	// a large limit).
	obs, err := e.obs.ListByProject(ctx, "", "", "", 10000)
	if err != nil {
		return stats, fmt.Errorf("list observations: %w", err)
	}
	tagMap := map[string][]memory.Observation{}
	for _, o := range obs {
		if err := e.writeObservation(ctx, o); err != nil {
			return stats, err
		}
		stats.Observations++
		for _, tag := range o.Tags {
			tagMap[tag] = append(tagMap[tag], o)
		}
	}

	// Sessions.
	sessions, err := e.sess.Recent(ctx, "", 10000)
	if err != nil {
		return stats, fmt.Errorf("list sessions: %w", err)
	}
	for _, s := range sessions {
		if err := e.writeSession(s); err != nil {
			return stats, err
		}
		stats.Sessions++
	}

	// Skills.
	sks, err := e.skill.List(ctx, "")
	if err != nil {
		return stats, fmt.Errorf("list skills: %w", err)
	}
	for _, sk := range sks {
		if err := e.writeSkill(sk); err != nil {
			return stats, err
		}
		stats.Skills++
	}

	// Tag MOCs.
	for tag, items := range tagMap {
		if err := e.writeTagMOC(tag, items); err != nil {
			return stats, err
		}
		stats.Tags++
	}

	// Dashboard.
	if err := e.writeIndex(sessions, sks, stats); err != nil {
		return stats, err
	}

	return stats, nil
}

// --- writers ------------------------------------------------------------

func (e *Exporter) writeObservation(ctx context.Context, o memory.Observation) error {
	name := obsFilename(o)
	path := filepath.Join(e.root, "observations", name)

	var b strings.Builder
	writeFrontmatter(&b, map[string]any{
		"id":         o.ID,
		"type":       string(o.Type),
		"importance": o.Importance,
		"tags":       o.Tags,
		"session":    o.SessionID,
		"project":    o.Project,
		"created":    o.CreatedAt.Format(time.RFC3339),
		"accessed":   o.AccessCount,
	})

	fmt.Fprintf(&b, "# %s\n\n", o.Title)
	if o.Rationale != "" {
		fmt.Fprintf(&b, "**Why:** %s\n\n", o.Rationale)
	}
	b.WriteString(o.Content)

	if o.SessionID != "" {
		fmt.Fprintf(&b, "\n\n## Session\n- [[%s]]\n", sessionLink(o.SessionID))
	}
	if len(o.Tags) > 0 {
		b.WriteString("\n## Tags\n")
		for _, t := range o.Tags {
			fmt.Fprintf(&b, "- [[tags/%s]]\n", slug(t))
		}
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	_ = e.obs.MarkExported(ctx, o.ID, time.Now().UTC())
	return nil
}

func (e *Exporter) writeSession(s session.Session) error {
	name := sessFilename(s)
	path := filepath.Join(e.root, "sessions", name)

	var b strings.Builder
	writeFrontmatter(&b, map[string]any{
		"id":      s.ID,
		"project": s.Project,
		"status":  string(s.Status),
		"started": s.StartedAt.Format(time.RFC3339),
		"ended":   timePtr(s.EndedAt),
	})
	fmt.Fprintf(&b, "# %s\n\n", defaultTitle(s.Goal, s.ID))
	if s.Summary != "" {
		fmt.Fprintf(&b, "## Summary\n%s\n\n", s.Summary)
	}
	if s.Reflection != "" {
		fmt.Fprintf(&b, "## Reflection\n%s\n", s.Reflection)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func (e *Exporter) writeSkill(sk skills.Skill) error {
	path := filepath.Join(e.root, "skills", slug(sk.Name)+".md")
	var b strings.Builder
	writeFrontmatter(&b, map[string]any{
		"id":            sk.ID,
		"version":       sk.Version,
		"use_count":     sk.UseCount,
		"effectiveness": sk.Effectiveness,
		"tags":          sk.Tags,
		"created":       sk.CreatedAt.Format(time.RFC3339),
		"updated":       sk.UpdatedAt.Format(time.RFC3339),
	})
	fmt.Fprintf(&b, "# %s\n\n", sk.Name)
	fmt.Fprintf(&b, "%s\n\n", sk.Description)
	fmt.Fprintf(&b, "## Procedure\n%s\n", sk.Procedure)
	if sk.Pitfalls != "" {
		fmt.Fprintf(&b, "\n## Pitfalls\n%s\n", sk.Pitfalls)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func (e *Exporter) writeTagMOC(tag string, items []memory.Observation) error {
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	path := filepath.Join(e.root, "tags", slug(tag)+".md")
	var b strings.Builder
	writeFrontmatter(&b, map[string]any{"tag": tag, "count": len(items)})
	fmt.Fprintf(&b, "# Tag: %s\n\n", tag)
	for _, o := range items {
		fmt.Fprintf(&b, "- [[observations/%s|%s]]\n", strings.TrimSuffix(obsFilename(o), ".md"), o.Title)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func (e *Exporter) writeIndex(sessions []session.Session, sks []skills.Skill, stats ExportStats) error {
	path := filepath.Join(e.root, "_index.md")
	var b strings.Builder
	b.WriteString("# Mnemos Vault\n\n")
	fmt.Fprintf(&b, "%d observations · %d sessions · %d skills · %d tags\n\n",
		stats.Observations, stats.Sessions, stats.Skills, stats.Tags)
	b.WriteString("## Recent sessions\n")
	for i, s := range sessions {
		if i >= 10 {
			break
		}
		fmt.Fprintf(&b, "- [[sessions/%s|%s]]\n", strings.TrimSuffix(sessFilename(s), ".md"), defaultTitle(s.Goal, s.ID))
	}
	b.WriteString("\n## Skills\n")
	for _, sk := range sks {
		fmt.Fprintf(&b, "- [[skills/%s|%s]] — %s\n", slug(sk.Name), sk.Name, sk.Description)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// --- helpers ------------------------------------------------------------

func obsFilename(o memory.Observation) string {
	date := o.CreatedAt.Format("2006-01-02")
	name := fmt.Sprintf("%s-%s.md", date, slug(o.Title))
	// Collision-safe suffix from ULID tail.
	if len(o.ID) >= 6 {
		name = fmt.Sprintf("%s-%s-%s.md", date, slug(o.Title), strings.ToLower(o.ID[len(o.ID)-6:]))
	}
	return name
}

func sessFilename(s session.Session) string {
	date := s.StartedAt.Format("2006-01-02")
	return fmt.Sprintf("%s-%s.md", date, slug(defaultTitle(s.Goal, s.ID)))
}

func sessionLink(id string) string { return "sessions/" + id }

func defaultTitle(goal, id string) string {
	if goal != "" {
		return goal
	}
	return "session-" + id
}

func slug(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = out[:60]
	}
	if out == "" {
		out = "untitled"
	}
	return out
}

func writeFrontmatter(b *strings.Builder, m map[string]any) {
	b.WriteString("---\n")
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := m[k]
		if v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			if val == "" {
				continue
			}
			fmt.Fprintf(b, "%s: %s\n", k, yamlEscape(val))
		case []string:
			if len(val) == 0 {
				continue
			}
			fmt.Fprintf(b, "%s: [%s]\n", k, strings.Join(quoteAll(val), ", "))
		case int, int64, float64:
			fmt.Fprintf(b, "%s: %v\n", k, val)
		case bool:
			fmt.Fprintf(b, "%s: %v\n", k, val)
		default:
			fmt.Fprintf(b, "%s: %v\n", k, val)
		}
	}
	b.WriteString("---\n\n")
}

func quoteAll(items []string) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = yamlEscape(s)
	}
	return out
}

func yamlEscape(s string) string {
	if strings.ContainsAny(s, ":#\n\"'[]{}") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func timePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339)
}
