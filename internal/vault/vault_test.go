package vault_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/internal/vault"
)

func newVault(t *testing.T) (*vault.Exporter, *memory.Service, *session.Service, *skills.Service, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mem := memory.NewService(memory.Config{Store: db.Observations()})
	sess := session.NewService(session.Config{Store: db.Sessions()})
	skl := skills.NewService(skills.Config{Store: db.Skills()})

	root := filepath.Join(dir, "vault")
	ex := vault.NewExporter(vault.Config{
		Root:     root,
		Obs:      db.Observations(),
		Sessions: db.Sessions(),
		Skills:   db.Skills(),
	})
	return ex, mem, sess, skl, root
}

func TestExportWritesVaultStructure(t *testing.T) {
	ex, mem, sess, skl, root := newVault(t)
	ctx := context.Background()

	opened, _ := sess.Open(ctx, session.OpenInput{Project: "mnemos", Goal: "ship v0.1"})
	_, _ = mem.Save(ctx, memory.SaveInput{
		SessionID: opened.ID,
		Project:   "mnemos",
		Title:     "use WAL mode",
		Content:   "Enable WAL for concurrent readers.",
		Type:      memory.TypePattern,
		Tags:      []string{"sqlite"},
	})
	_, _ = skl.Save(ctx, skills.SaveInput{
		Name:        "wire-mcp-tool",
		Description: "add a new mcp tool",
		Procedure:   "1. define\n2. register\n3. test",
	})

	stats, err := ex.ExportAll(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if stats.Observations < 1 || stats.Sessions < 1 || stats.Skills < 1 || stats.Tags < 1 {
		t.Errorf("unexpected stats: %+v", stats)
	}

	// Directory structure.
	for _, sub := range []string{"observations", "sessions", "skills", "tags"} {
		if _, err := os.Stat(filepath.Join(root, sub)); err != nil {
			t.Errorf("missing %s dir: %v", sub, err)
		}
	}
	// _index.md exists.
	idx, err := os.ReadFile(filepath.Join(root, "_index.md"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.Contains(string(idx), "Mnemos Vault") {
		t.Errorf("index content wrong: %q", idx)
	}
}

func TestObservationHasFrontmatterAndSessionLink(t *testing.T) {
	ex, mem, sess, _, root := newVault(t)
	ctx := context.Background()

	opened, _ := sess.Open(ctx, session.OpenInput{Goal: "fix-bug"})
	_, _ = mem.Save(ctx, memory.SaveInput{
		SessionID: opened.ID,
		Title:     "Example observation",
		Content:   "body here",
		Type:      memory.TypeDecision,
		Rationale: "because X",
		Tags:      []string{"alpha", "beta"},
	})

	_, err := ex.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(root, "observations"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no observations written")
	}
	body, _ := os.ReadFile(filepath.Join(root, "observations", entries[0].Name()))
	text := string(body)
	if !strings.HasPrefix(text, "---\n") {
		t.Error("expected YAML frontmatter")
	}
	if !strings.Contains(text, "**Why:** because X") {
		t.Error("missing rationale section")
	}
	if !strings.Contains(text, "[[sessions/") {
		t.Error("missing session wikilink")
	}
}
