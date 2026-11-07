package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func TestSessionRetrievalArmRoundTrips(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := db.Sessions()
	now := time.Now().UTC()

	if err := store.Insert(ctx, &session.Session{
		ID: "with-arm", AgentID: "a", StartedAt: now, RetrievalArm: session.ArmIndex,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(ctx, &session.Session{ID: "no-arm", AgentID: "a", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore(ctx, &session.Session{
		ID: "restored", AgentID: "a", StartedAt: now, RetrievalArm: session.ArmPush,
	}); err != nil {
		t.Fatal(err)
	}

	for id, want := range map[string]session.RetrievalArm{
		"with-arm": session.ArmIndex,
		"no-arm":   "",
		"restored": session.ArmPush,
	} {
		got, err := store.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.RetrievalArm != want {
			t.Errorf("%s: arm %q, want %q", id, got.RetrievalArm, want)
		}
	}
}

func TestAssignedConversations(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := db.Sessions()
	now := time.Now().UTC()

	insert := func(id, host string, arm session.RetrievalArm, age time.Duration) {
		t.Helper()
		if err := store.Insert(ctx, &session.Session{
			ID: id, AgentID: "a", Project: "p", HostSessionID: host,
			RetrievalArm: arm, StartedAt: now.Add(-age),
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert("s1", "host-a", session.ArmPush, 2*time.Hour)
	insert("s2", "host-a", session.ArmPush, time.Hour) // the same conversation, resumed
	insert("s3", "host-b", session.ArmIndex, 30*time.Minute)
	insert("s4", "host-old", session.ArmIndex, 72*time.Hour) // before the cutoff
	insert("s5", "", session.ArmIndex, time.Minute)          // no host, so no transcript
	insert("s6", "host-none", "", time.Minute)               // never assigned

	got, err := db.AssignedConversations(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want host-a and host-b once each, got %+v", got)
	}
	if got[0].HostID != "host-a" || got[0].Arm != "push" || got[0].Project != "p" {
		t.Errorf("first: %+v", got[0])
	}
	if got[1].HostID != "host-b" || got[1].Arm != "index" {
		t.Errorf("second: %+v", got[1])
	}
}
