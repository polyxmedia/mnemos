package storage_test

import (
	"context"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
)

func TestMigration0003AddsProvenanceColumns(t *testing.T) {
	db := openTestDB(t)

	// Every new DB must carry all three provenance columns after migration.
	// pragma_table_info is the portable way to enumerate columns on SQLite.
	expected := map[string]bool{
		"source_kind":  false,
		"trust_tier":   false,
		"derived_from": false,
	}
	rows, err := db.SQL().Query(`SELECT name FROM pragma_table_info('observations')`)
	if err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if _, ok := expected[name]; ok {
			expected[name] = true
		}
	}
	for col, seen := range expected {
		if !seen {
			t.Errorf("observations.%s column missing after migration", col)
		}
	}
}

func TestMigration0003DefaultsExistingRows(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Insert using the service to exercise the end-to-end path, then read
	// back with a raw SQL query to prove the DEFAULT clauses produced
	// valid CHECK-constrained values without the Go layer intervening.
	o := newObs("", "legacy row", "pretend I existed before migration 0003", memory.TypeDecision)
	if err := db.Observations().Insert(ctx, o); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var sourceKind, trustTier, derivedFrom string
	err := db.SQL().QueryRow(
		`SELECT source_kind, trust_tier, derived_from FROM observations WHERE id = ?`,
		o.ID,
	).Scan(&sourceKind, &trustTier, &derivedFrom)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if sourceKind != "user" {
		t.Errorf("default source_kind should be 'user', got %q", sourceKind)
	}
	if trustTier != "curated" {
		t.Errorf("default trust_tier should be 'curated', got %q", trustTier)
	}
	if derivedFrom != "[]" {
		t.Errorf("default derived_from should be '[]', got %q", derivedFrom)
	}
}

func TestMigration0003CheckConstraintsReject(t *testing.T) {
	db := openTestDB(t)

	// Direct raw insert bypasses the Go layer so we can exercise the CHECK
	// clause honestly. A bogus source_kind must be refused at the SQL level.
	_, err := db.SQL().Exec(
		`INSERT INTO observations
			(id, agent_id, title, content, obs_type, source_kind)
			VALUES ('raw1', 'default', 't', 'c', 'decision', 'alien')`,
	)
	if err == nil {
		t.Error("CHECK on source_kind must reject 'alien'")
	}

	_, err = db.SQL().Exec(
		`INSERT INTO observations
			(id, agent_id, title, content, obs_type, trust_tier)
			VALUES ('raw2', 'default', 't', 'c', 'decision', 'ghost')`,
	)
	if err == nil {
		t.Error("CHECK on trust_tier must reject 'ghost'")
	}
}
