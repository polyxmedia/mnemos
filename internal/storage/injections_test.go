package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func openInjDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestInjectionsRecordAndListByRef(t *testing.T) {
	db := openInjDB(t)
	store := db.Injections()
	ctx := context.Background()

	// Explicit Go timestamps: ListByRef orders by created_at and the
	// events land within the same second, so CURRENT_TIMESTAMP-style
	// second precision would make the ordering assertion flaky.
	base := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	events := []injection.Event{
		{
			ID: ulid.Make().String(), Kind: injection.KindObservation, RefID: "obs-1",
			Channel: injection.ChannelPrewarm, AgentID: "default",
			Project: "mnemos", SessionID: "sess-1", CreatedAt: base,
		},
		{
			ID: ulid.Make().String(), Kind: injection.KindObservation, RefID: "obs-1",
			Channel: injection.ChannelPromptHook, AgentID: "default",
			Project: "mnemos", SessionID: "sess-2", CreatedAt: base.Add(500 * time.Millisecond),
		},
		{
			ID: ulid.Make().String(), Kind: injection.KindSkill, RefID: "skill-1",
			Channel: injection.ChannelPrewarm, CreatedAt: base,
		},
	}
	if err := store.Record(ctx, events); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := store.ListByRef(ctx, injection.KindObservation, "obs-1", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 events for obs-1, got %d", len(got))
	}
	if got[0].Channel != injection.ChannelPromptHook {
		t.Errorf("want newest first (prompt_hook), got %s", got[0].Channel)
	}
	if got[0].SessionID != "sess-2" || got[1].SessionID != "sess-1" {
		t.Errorf("session ids lost in round trip: %q, %q", got[0].SessionID, got[1].SessionID)
	}
	if got[1].Project != "mnemos" || got[1].AgentID != "default" {
		t.Errorf("fields lost in round trip: %+v", got[1])
	}

	skills, err := store.ListByRef(ctx, injection.KindSkill, "skill-1", 10)
	if err != nil {
		t.Fatalf("list skill: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("want 1 skill event, got %d", len(skills))
	}
	// Empty project/session round-trip as empty, not as a scan error.
	if skills[0].Project != "" || skills[0].SessionID != "" {
		t.Errorf("empty optionals must stay empty, got %+v", skills[0])
	}
}

func TestInjectionsRecordEmptyIsNoop(t *testing.T) {
	db := openInjDB(t)
	if err := db.Injections().Record(context.Background(), nil); err != nil {
		t.Fatalf("empty record must be a no-op, got %v", err)
	}
}

func TestInjectionsRejectsUnknownKindAndChannel(t *testing.T) {
	db := openInjDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	bad := []injection.Event{{
		ID: ulid.Make().String(), Kind: "session", RefID: "x",
		Channel: injection.ChannelPrewarm, CreatedAt: now,
	}}
	if err := db.Injections().Record(ctx, bad); err == nil {
		t.Error("unknown kind must violate the CHECK constraint")
	}

	bad = []injection.Event{{
		ID: ulid.Make().String(), Kind: injection.KindObservation, RefID: "x",
		Channel: "carrier_pigeon", CreatedAt: now,
	}}
	if err := db.Injections().Record(ctx, bad); err == nil {
		t.Error("unknown channel must violate the CHECK constraint")
	}
}

func TestInjectionLoggerStampsAndSkipsEmptyRefs(t *testing.T) {
	db := openInjDB(t)
	ctx := context.Background()

	fixed := time.Date(2026, 6, 10, 12, 0, 0, 123456000, time.UTC)
	logger := injection.NewLogger(db.Injections(), func() time.Time { return fixed })

	err := logger.Log(ctx, injection.ChannelContext, "default", "mnemos", "sess-1", []injection.Ref{
		{Kind: injection.KindObservation, ID: "obs-9"},
		{Kind: injection.KindObservation, ID: ""}, // skipped
	})
	if err != nil {
		t.Fatalf("log: %v", err)
	}

	got, err := db.Injections().ListByRef(ctx, injection.KindObservation, "obs-9", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	if got[0].ID == "" {
		t.Error("logger must assign an event ID")
	}
	if !got[0].CreatedAt.Equal(fixed) {
		t.Errorf("want clock-stamped %v, got %v", fixed, got[0].CreatedAt)
	}
}

func TestInjectionLoggerNilReceiverIsSafe(t *testing.T) {
	var logger *injection.Logger
	err := logger.Log(context.Background(), injection.ChannelPrewarm, "", "", "", []injection.Ref{
		{Kind: injection.KindObservation, ID: "obs-1"},
	})
	if err != nil {
		t.Fatalf("nil logger must be a no-op, got %v", err)
	}
}
