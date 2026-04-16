package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/skills"
)

func TestSkillExportImportRoundTrip(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	// Seed one skill via the service so export has something to pack.
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.skl.Save(ctx, skills.SaveInput{
		Name:        "shared-skill",
		Description: "a skill worth sharing",
		Procedure:   "1. call mnemos_skill_save\n2. share the pack",
		Tags:        []string{"meta"},
	}); err != nil {
		t.Fatal(err)
	}
	d.close()

	home := os.Getenv("HOME")
	packPath := filepath.Join(home, "pack.json")

	// Export to file.
	if err := runSkill(ctx, []string{"export",
		"--out", packPath,
		"--source", "@testuser",
	}); err != nil {
		t.Fatalf("export: %v", err)
	}

	// Verify the pack file is valid JSON and contains our skill.
	body, err := os.ReadFile(packPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "@testuser") {
		t.Errorf("source attribution missing from pack: %s", body)
	}

	// Wipe skills by moving to a fresh HOME, then import.
	withHome(t)
	if err := runSkillImport(ctx, []string{packPath}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Confirm it landed.
	out := captureStdout(t, func() {
		_ = runSkillList(ctx, nil)
	})
	if !strings.Contains(out, "shared-skill") {
		t.Errorf("imported skill not listed: %s", out)
	}
}

func TestSkillImportFromHTTPURL(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	// Serve a canned pack.
	pack := `{"version":1,"created_at":"2026-04-16T00:00:00Z","skills":[` +
		`{"name":"remote-skill","description":"d","procedure":"1. do"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(pack))
	}))
	defer srv.Close()

	if err := runSkillImport(ctx, []string{srv.URL + "/pack.json"}); err != nil {
		t.Fatalf("import via URL: %v", err)
	}

	out := captureStdout(t, func() {
		_ = runSkillList(ctx, nil)
	})
	if !strings.Contains(out, "remote-skill") {
		t.Errorf("remote-imported skill not listed: %s", out)
	}
}

func TestSkillImportRejectsBadURL(t *testing.T) {
	withHome(t)
	// 404 → error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if err := runSkillImport(context.Background(),
		[]string{srv.URL + "/missing.json"}); err == nil {
		t.Error("404 URL must error")
	}
}

func TestSkillImportRejectsMalformedPack(t *testing.T) {
	withHome(t)
	home := os.Getenv("HOME")
	bad := filepath.Join(home, "bad.json")
	_ = os.WriteFile(bad, []byte(`{not: valid}`), 0o600)
	if err := runSkillImport(context.Background(), []string{bad}); err == nil {
		t.Error("malformed pack must error")
	}
}

func TestSkillExportNoSkillsErrors(t *testing.T) {
	withHome(t)
	if err := runSkillExport(context.Background(), nil); err == nil {
		t.Error("export with no skills must error")
	}
}

func TestSkillListEmpty(t *testing.T) {
	withHome(t)
	out := captureStdout(t, func() {
		_ = runSkillList(context.Background(), nil)
	})
	if !strings.Contains(out, "no skills saved yet") {
		t.Errorf("expected 'no skills saved yet', got: %s", out)
	}
}

func TestSkillUnknownSubcommand(t *testing.T) {
	if err := runSkill(context.Background(), []string{"teleport"}); err == nil {
		t.Error("unknown subcommand must error")
	}
	if err := runSkill(context.Background(), nil); err == nil {
		t.Error("empty args must error")
	}
}

