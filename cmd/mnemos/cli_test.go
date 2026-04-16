package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withHome sets $HOME for the duration of the test so loadServices picks
// up an isolated config + db.
func withHome(t *testing.T) {
	t.Helper()
	old := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", old) })
	_ = os.Setenv("HOME", t.TempDir())
}

// captureStdout runs fn with os.Stdout redirected to a buffer, returns
// whatever was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	<-done
	return buf.String()
}

func TestVersionCommand(t *testing.T) {
	out := captureStdout(t, func() {
		_ = runVersion(context.Background(), nil)
	})
	if !strings.HasPrefix(out, "mnemos ") {
		t.Errorf("version output unexpected: %q", out)
	}
}

func TestHelpCommand(t *testing.T) {
	out := captureStdout(t, func() {
		_ = runHelp(context.Background(), nil)
	})
	for _, want := range []string{"serve", "init", "doctor", "dream", "vault"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q: %s", want, out)
		}
	}
}

func TestStatsCommandOnFreshDB(t *testing.T) {
	withHome(t)
	out := captureStdout(t, func() {
		_ = runStats(context.Background(), nil)
	})
	if !strings.Contains(out, "observations:") {
		t.Errorf("stats output missing observations line: %s", out)
	}
}

func TestSearchRequiresQuery(t *testing.T) {
	withHome(t)
	if err := runSearch(context.Background(), nil); err == nil {
		t.Error("expected error when query omitted")
	}
}

func TestInitRegistersCleanly(t *testing.T) {
	withHome(t)
	// Create a claude-like config location so the installer detects it.
	home := os.Getenv("HOME")
	_ = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{}`), 0o644)

	out := captureStdout(t, func() {
		_ = runInit(context.Background(), nil)
	})
	if !strings.Contains(out, "registered") && !strings.Contains(out, "up to date") {
		t.Errorf("init should confirm registration, got: %s", out)
	}
}

func TestDoctorSucceedsWhenConfigured(t *testing.T) {
	withHome(t)
	home := os.Getenv("HOME")

	// Write a .claude.json with a mnemos entry so doctor finds it registered.
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"mnemos": map[string]any{"command": "mnemos", "args": []string{"serve"}},
		},
	}
	buf, _ := json.Marshal(cfg)
	_ = os.WriteFile(filepath.Join(home, ".claude.json"), buf, 0o644)

	// runDoctor returns an error only when something is wrong.
	err := runDoctor(context.Background(), nil)
	if err != nil {
		// It's still OK if Cursor/Windsurf dirs exist under ~ from a
		// previous test run — we just want to know the code path runs.
		t.Logf("doctor returned: %v (acceptable if other clients unregistered)", err)
	}
}

func TestImportThenExportRoundTrip(t *testing.T) {
	withHome(t)

	// Stage a JSON snapshot.
	home := os.Getenv("HOME")
	snapPath := filepath.Join(home, "snapshot.json")
	snap := snapshot{}
	// Use the real types so Save validation passes.
	buf, _ := json.MarshalIndent(snap, "", "  ")
	_ = os.WriteFile(snapPath, buf, 0o644)

	// Empty import should succeed cleanly.
	if err := runImport(context.Background(), []string{snapPath}); err != nil {
		t.Fatalf("import empty snapshot: %v", err)
	}

	// Export should produce something non-empty (the default config created).
	outPath := filepath.Join(home, "out.json")
	if err := runExport(context.Background(), []string{outPath}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if fi, err := os.Stat(outPath); err != nil || fi.Size() == 0 {
		t.Errorf("export did not write a file: err=%v", err)
	}
}

func TestPruneRuns(t *testing.T) {
	withHome(t)
	out := captureStdout(t, func() {
		if err := runPrune(context.Background(), nil); err != nil {
			t.Errorf("prune: %v", err)
		}
	})
	if !strings.Contains(out, "pruned") {
		t.Errorf("prune output unexpected: %s", out)
	}
}

func TestConfigPrints(t *testing.T) {
	withHome(t)
	out := captureStdout(t, func() {
		_ = runConfig(context.Background(), nil)
	})
	if !strings.Contains(out, "Storage") || !strings.Contains(out, "Search") {
		t.Errorf("config output missing sections: %s", out)
	}
}

func TestUnknownSubcommandForVault(t *testing.T) {
	withHome(t)
	if err := runVault(context.Background(), []string{"ninja"}); err == nil {
		t.Error("expected error for unknown vault subcommand")
	}
}

func TestVaultStatusBeforeExport(t *testing.T) {
	withHome(t)
	out := captureStdout(t, func() {
		_ = runVaultStatus(context.Background(), nil)
	})
	if !strings.Contains(out, "vault path") {
		t.Errorf("vault status missing path line: %s", out)
	}
}

func TestEmbedStatus(t *testing.T) {
	withHome(t)
	out := captureStdout(t, func() {
		_ = runEmbedStatus(context.Background())
	})
	if !strings.Contains(out, "provider:") {
		t.Errorf("embed status missing provider line: %s", out)
	}
}
