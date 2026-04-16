package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

func TestObsDeleteMissingReturnsNotFound(t *testing.T) {
	db := openObsDB(t)
	err := db.Observations().Delete(context.Background(), "nope")
	if err == nil {
		t.Error("delete unknown must error")
	}
}

func TestDecayImportanceReducesValues(t *testing.T) {
	db := openObsDB(t)
	store := db.Observations()
	ctx := context.Background()

	// Insert with a very old last_accessed_at via direct SQL so it hits
	// the decay cutoff. (Public API only inserts with CURRENT_TIMESTAMP.)
	o := &memory.Observation{
		ID: ulid.Make().String(), AgentID: "default", Project: "p",
		Title: "stale", Content: "content", Type: memory.TypePattern,
		Importance: 9, CreatedAt: time.Now().AddDate(0, -6, 0),
		ValidFrom: time.Now().AddDate(0, -6, 0),
	}
	if err := store.Insert(ctx, o); err != nil {
		t.Fatal(err)
	}
	// Force created_at into the past to qualify as stale.
	_, _ = db.SQL().ExecContext(ctx,
		`UPDATE observations SET created_at = ?, last_accessed_at = NULL WHERE id = ?`,
		time.Now().AddDate(0, -6, 0), o.ID)

	n, err := store.DecayImportance(ctx, 30, 2)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("expected at least one row decayed")
	}

	got, _ := store.Get(ctx, o.ID)
	if got.Importance >= 9 {
		t.Errorf("importance should have dropped from 9, got %d", got.Importance)
	}
	if got.Importance < 1 {
		t.Errorf("importance should never drop below 1, got %d", got.Importance)
	}
}

func TestDecayWithZeroParamsIsNoop(t *testing.T) {
	db := openObsDB(t)
	n, _ := db.Observations().DecayImportance(context.Background(), 0, 1)
	if n != 0 {
		t.Errorf("zero stale_days should be noop, got %d", n)
	}
	n, _ = db.Observations().DecayImportance(context.Background(), 30, 0)
	if n != 0 {
		t.Errorf("zero amount should be noop, got %d", n)
	}
}

func TestSkillsListAndCount(t *testing.T) {
	db := openObsDB(t)
	store := db.Skills()
	ctx := context.Background()

	for _, name := range []string{"a", "b", "c"} {
		if _, err := store.Upsert(ctx, skills.SaveInput{
			Name: name, Description: "d", Procedure: "p",
		}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := store.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Errorf("want 3 skills, got %d", len(list))
	}
	count, err := store.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("want count 3, got %d", count)
	}

	scoped, _ := store.List(ctx, "someagent")
	if len(scoped) != 0 {
		t.Errorf("unknown agent should get 0 skills, got %d", len(scoped))
	}
}

func TestStorageRecentSessions(t *testing.T) {
	db := openObsDB(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		s := &session.Session{
			ID: ulid.Make().String(), AgentID: "default",
			Project: "p", Goal: "g", StartedAt: time.Now().UTC(),
		}
		_ = db.Sessions().Insert(ctx, s)
	}
	st, err := db.Observations().Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RecentSessions) != 3 {
		t.Errorf("want 3 recent sessions in stats, got %d", len(st.RecentSessions))
	}
}
