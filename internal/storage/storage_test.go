package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "mnemos.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newObs(sessionID, title, content string, typ memory.ObsType) *memory.Observation {
	now := time.Now().UTC()
	return &memory.Observation{
		ID:         ulid.Make().String(),
		SessionID:  sessionID,
		AgentID:    "default",
		Project:    "mnemos",
		Title:      title,
		Content:    content,
		Type:       typ,
		Tags:       []string{"go", "sqlite"},
		Importance: 7,
		CreatedAt:  now,
		ValidFrom:  now,
	}
}

func TestMigrationsApplyCleanly(t *testing.T) {
	db := openTestDB(t)
	var count int
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if count == 0 {
		t.Fatal("expected at least one applied migration")
	}
}

func TestObservationCRUD(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	ctx := context.Background()

	o := newObs("", "Use WAL mode", "Enable WAL to allow concurrent readers.", memory.TypePattern)
	if err := store.Insert(ctx, o); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.Get(ctx, o.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != o.Title {
		t.Errorf("title mismatch: got %q want %q", got.Title, o.Title)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "go" {
		t.Errorf("tags mismatch: got %v", got.Tags)
	}

	// Access count bumped by Get.
	got2, err := store.Get(ctx, o.ID)
	if err != nil {
		t.Fatalf("get 2: %v", err)
	}
	if got2.AccessCount < 1 {
		t.Errorf("expected access count bump, got %d", got2.AccessCount)
	}
}

func TestSearchBM25(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	ctx := context.Background()

	items := []*memory.Observation{
		newObs("", "WAL mode", "SQLite write-ahead log unlocks concurrent reads.", memory.TypePattern),
		newObs("", "FTS5 tokenizer", "Use porter+unicode61 for English-ish content.", memory.TypePattern),
		newObs("", "Session end", "Always write a reflection at session_end.", memory.TypePreference),
	}
	for _, o := range items {
		if err := store.Insert(ctx, o); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	results, err := store.Search(ctx, memory.SearchInput{Query: "wal", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].Observation.Title != "WAL mode" {
		t.Errorf("expected 1 WAL hit, got %d results", len(results))
	}
	if results[0].Score <= 0 {
		t.Errorf("expected positive BM25 score, got %f", results[0].Score)
	}
	if results[0].Snippet == "" {
		t.Error("expected non-empty snippet")
	}
}

func TestSearchExcludesInvalidatedByDefault(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	ctx := context.Background()

	o := newObs("", "Old rule", "We deploy on Fridays.", memory.TypePreference)
	if err := store.Insert(ctx, o); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.Invalidate(ctx, o.ID, time.Now().UTC()); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	results, err := store.Search(ctx, memory.SearchInput{Query: "fridays", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("invalidated observations must be hidden by default, got %d hits", len(results))
	}

	results, err = store.Search(ctx, memory.SearchInput{Query: "fridays", Limit: 10, IncludeStale: true})
	if err != nil {
		t.Fatalf("search stale: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("IncludeStale should surface invalidated observations, got %d", len(results))
	}
}

func TestSearchRespectsImportanceFloor(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	ctx := context.Background()

	low := newObs("", "Low signal", "minor note about WAL.", memory.TypeContext)
	low.Importance = 2
	high := newObs("", "High signal", "critical WAL pattern.", memory.TypePattern)
	high.Importance = 9

	if err := store.Insert(ctx, low); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(ctx, high); err != nil {
		t.Fatal(err)
	}

	results, err := store.Search(ctx, memory.SearchInput{
		Query:         "wal",
		MinImportance: 5,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].Observation.ID != high.ID {
		t.Errorf("importance floor not enforced")
	}
}

func TestSearchScopedByAgentAndProject(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	ctx := context.Background()

	a := newObs("", "Team A WAL", "WAL config for A.", memory.TypePattern)
	a.AgentID, a.Project = "alice", "proj-a"
	b := newObs("", "Team B WAL", "WAL config for B.", memory.TypePattern)
	b.AgentID, b.Project = "bob", "proj-b"
	for _, o := range []*memory.Observation{a, b} {
		if err := store.Insert(ctx, o); err != nil {
			t.Fatal(err)
		}
	}

	results, err := store.Search(ctx, memory.SearchInput{Query: "wal", AgentID: "alice", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Observation.AgentID != "alice" {
		t.Errorf("agent scoping failed, got %+v", results)
	}

	results, err = store.Search(ctx, memory.SearchInput{Query: "wal", Project: "proj-b", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Observation.Project != "proj-b" {
		t.Errorf("project scoping failed")
	}
}

func TestSupersessionInvalidatesTarget(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	ctx := context.Background()

	old := newObs("", "Use X", "We use X for logging.", memory.TypeDecision)
	newer := newObs("", "Use Y", "We use Y for logging now.", memory.TypeDecision)
	if err := store.Insert(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(ctx, newer); err != nil {
		t.Fatal(err)
	}

	if err := store.Link(ctx, newer.ID, old.ID, memory.LinkSupersedes); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := store.Invalidate(ctx, old.ID, time.Now().UTC()); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	results, err := store.Search(ctx, memory.SearchInput{Query: "logging", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Observation.ID != newer.ID {
		t.Errorf("expected only the newer fact, got %d results", len(results))
	}
}

func TestSessionLifecycle(t *testing.T) {
	db := openTestDB(t)
	sess := db.Sessions()
	ctx := context.Background()

	s := &session.Session{
		ID:        ulid.Make().String(),
		AgentID:   "default",
		Project:   "mnemos",
		Goal:      "ship the storage layer",
		StartedAt: time.Now().UTC(),
	}
	if err := sess.Insert(ctx, s); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	got, err := sess.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Goal != s.Goal {
		t.Errorf("goal mismatch")
	}

	if err := sess.Close(ctx, session.CloseInput{
		ID:         s.ID,
		Summary:    "storage compiles and tests pass",
		Reflection: "writing the schema first paid off — no retrofitting",
	}); err != nil {
		t.Fatalf("close: %v", err)
	}

	got, err = sess.Get(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EndedAt == nil {
		t.Error("expected ended_at set")
	}
	if got.Reflection == "" {
		t.Error("reflection missing")
	}

	current, err := sess.Current(ctx, "default")
	if err == nil {
		t.Errorf("expected ErrNotFound for current after close, got %v", current)
	}
}

func TestSkillUpsertVersioning(t *testing.T) {
	db := openTestDB(t)
	store := db.Skills()
	ctx := context.Background()

	first, err := store.Upsert(ctx, skills.SaveInput{
		AgentID:     "default",
		Name:        "add-api-route",
		Description: "Wire a new Next.js API route with auth.",
		Procedure:   "1. Create file. 2. Export handler. 3. Register route.",
		Tags:        []string{"next", "api"},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if first.Version != 1 {
		t.Errorf("first version should be 1, got %d", first.Version)
	}

	second, err := store.Upsert(ctx, skills.SaveInput{
		AgentID:     "default",
		Name:        "add-api-route",
		Description: "Wire a new Next.js API route with auth + rate limiting.",
		Procedure:   "1. Create file. 2. Export handler. 3. Middleware. 4. Register.",
		Tags:        []string{"next", "api", "rate-limit"},
	})
	if err != nil {
		t.Fatalf("upsert v2: %v", err)
	}
	if second.Version != 2 {
		t.Errorf("second version should be 2, got %d", second.Version)
	}
	if second.ID != first.ID {
		t.Errorf("upsert should keep the same ID")
	}

	matches, err := store.Match(ctx, skills.MatchInput{Query: "api route", Limit: 5})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(matches) != 1 || matches[0].Skill.ID != first.ID {
		t.Errorf("match failed: %+v", matches)
	}

	if err := store.RecordUse(ctx, skills.FeedbackInput{ID: first.ID, Success: true}); err != nil {
		t.Fatalf("record use: %v", err)
	}
	got, _ := store.Get(ctx, first.ID)
	if got.UseCount != 1 || got.Effectiveness != 1.0 {
		t.Errorf("feedback failed: use=%d eff=%f", got.UseCount, got.Effectiveness)
	}
}

func TestStatsCountsAreSane(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	obs := db.Observations()

	for i := 0; i < 5; i++ {
		o := newObs("", "item", "content about sqlite", memory.TypeContext)
		if err := obs.Insert(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	st, err := obs.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.Observations != 5 {
		t.Errorf("want 5 observations, got %d", st.Observations)
	}
	if st.LiveObservations != 5 {
		t.Errorf("want 5 live, got %d", st.LiveObservations)
	}
}

func TestPruneRemovesExpired(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	ctx := context.Background()

	past := time.Now().UTC().Add(-time.Hour)
	o := newObs("", "short-lived", "expired hint", memory.TypeContext)
	o.ExpiresAt = &past
	if err := store.Insert(ctx, o); err != nil {
		t.Fatal(err)
	}
	n, err := store.Prune(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 pruned, got %d", n)
	}
	if _, err := store.Get(ctx, o.ID); err == nil {
		t.Error("expected ErrNotFound after prune")
	}
}
