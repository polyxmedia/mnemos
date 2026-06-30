package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// TestExportImportRoundTripPreservesFidelity drives the real CLI commands:
// seed store A, `mnemos export` to a file, then `mnemos import` into a fresh
// store B, and assert identity + provenance survived the round trip. This is
// the end-to-end guard behind the portable-export capability.
func TestExportImportRoundTripPreservesFidelity(t *testing.T) {
	ctx := context.Background()
	dumpFile := filepath.Join(t.TempDir(), "dump.json")

	// Store A: seed a tool-sourced observation (clamped to the raw tier) and
	// export it.
	homeWithConfig(t, "")
	var savedID string
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.mem.Save(ctx, memory.SaveInput{
		AgentID: "agentX", Project: "projX", Title: "retry 401",
		Content: "401 is auth, refresh the token", Type: memory.TypeCorrection,
		Tags: []string{"auth"}, Importance: 9, SourceKind: memory.SourceTool,
	})
	if err != nil {
		t.Fatal(err)
	}
	savedID = res.Observation.ID
	d.close()

	if err := runExport(ctx, []string{dumpFile}); err != nil {
		t.Fatalf("export: %v", err)
	}

	// Store B: a fresh, isolated home. Import the dump.
	homeWithConfig(t, "")
	out := captureStdout(t, func() {
		if err := runImport(ctx, []string{dumpFile}); err != nil {
			t.Fatalf("import: %v", err)
		}
	})
	if !strings.Contains(out, "observations 1 added") {
		t.Errorf("import summary should report 1 observation added, got: %s", out)
	}

	d2, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.close()
	got, err := d2.mem.Get(ctx, savedID)
	if err != nil {
		t.Fatalf("restored observation %s not found in store B: %v", savedID, err)
	}
	if got.ID != savedID {
		t.Errorf("id not preserved: got %q want %q", got.ID, savedID)
	}
	if got.SourceKind != memory.SourceTool || got.TrustTier != memory.TrustRaw {
		t.Errorf("provenance not preserved through CLI round trip: source_kind=%v trust_tier=%v", got.SourceKind, got.TrustTier)
	}
	if got.Importance != 9 || got.Type != memory.TypeCorrection {
		t.Errorf("fields not preserved: importance=%d type=%v", got.Importance, got.Type)
	}
}
