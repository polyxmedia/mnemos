package dream

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// gateFixture wires the outcome stores (Sessions + Injections) into the
// dream service, unlike promoteFixture which exercises the legacy
// frequency-only path.
type gateFixture struct {
	ds   *Service
	mem  *memory.Service
	sess *session.Service
	sk   *skills.Service
	db   *storage.DB
}

func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mem := memory.NewService(memory.Config{Store: db.Observations()})
	sess := session.NewService(session.Config{Store: db.Sessions()})
	sk := skills.NewService(skills.Config{Store: db.Skills()})
	ds := NewService(Config{
		Memory:     mem,
		Store:      db.Observations(),
		Reader:     db.Observations(),
		Skills:     sk,
		Sessions:   db.Sessions(),
		Injections: db.Injections(),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		StaleDays:  1,
	})
	return &gateFixture{ds: ds, mem: mem, sess: sess, sk: sk, db: db}
}

// openSession opens a session for the project and returns its ID.
func (f *gateFixture) openSession(t *testing.T, project string) string {
	t.Helper()
	s, err := f.sess.Open(context.Background(), session.OpenInput{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	return s.ID
}

// seedCorrection writes one correction bound to an existing session and
// returns the observation ID.
func (f *gateFixture) seedCorrection(t *testing.T, project, tag, title, sessID string) string {
	t.Helper()
	structured, _ := json.Marshal(correctionData{
		Tried: "the wrong way", WrongBecause: "it broke", Fix: "the right way",
		TriggerContext: "when doing " + tag,
	})
	res, err := f.mem.Save(context.Background(), memory.SaveInput{
		Title: title, Content: "tried X, was wrong because Y, fix Z",
		Type: memory.TypeCorrection, Project: project,
		Tags: []string{tag}, SessionID: sessID,
		Structured: string(structured),
	})
	if err != nil {
		t.Fatal(err)
	}
	return res.Observation.ID
}

func (f *gateFixture) skillCount(t *testing.T) int {
	t.Helper()
	list, err := f.sk.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func TestGateDefersSingleSessionBurst(t *testing.T) {
	f := newGateFixture(t)
	ctx := context.Background()

	sessID := f.openSession(t, "api")
	for _, title := range []string{"oauth retry a", "oauth retry b", "oauth retry c"} {
		f.seedCorrection(t, "api", "oauth", title, sessID)
	}

	promoted, deferred, err := f.ds.promoteSkillsFromCorrections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if promoted != 0 {
		t.Errorf("single-session burst must not promote, got %d", promoted)
	}
	if deferred != 1 {
		t.Errorf("want 1 deferred cluster, got %d", deferred)
	}
	if n := f.skillCount(t); n != 0 {
		t.Errorf("no skill should exist, got %d", n)
	}
}

func TestGateDefersWithoutOutcomeEvidence(t *testing.T) {
	f := newGateFixture(t)
	ctx := context.Background()

	// Three corrections across three ok sessions: recurrence spread holds
	// but nothing shows the mistake had cost or survived being surfaced.
	for _, title := range []string{"oauth retry a", "oauth retry b", "oauth retry c"} {
		sessID := f.openSession(t, "api")
		f.seedCorrection(t, "api", "oauth", title, sessID)
	}

	promoted, deferred, err := f.ds.promoteSkillsFromCorrections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if promoted != 0 || deferred != 1 {
		t.Errorf("want 0 promoted / 1 deferred, got %d / %d", promoted, deferred)
	}
}

func TestGateAdmitsRecurrenceAfterSurfacing(t *testing.T) {
	f := newGateFixture(t)
	ctx := context.Background()

	s1 := f.openSession(t, "api")
	s2 := f.openSession(t, "api")
	s3 := f.openSession(t, "api")
	first := f.seedCorrection(t, "api", "oauth", "oauth retry a", s1)
	f.seedCorrection(t, "api", "oauth", "oauth retry b", s2)

	// The first correction gets surfaced into context...
	err := f.db.Injections().Record(ctx, []injection.Event{{
		ID: ulid.Make().String(), Kind: injection.KindObservation, RefID: first,
		Channel: injection.ChannelPrewarm, Project: "api",
		CreatedAt: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	// ...and the mistake STILL recurs afterwards. Passive memory lost;
	// the cluster has earned a skill.
	f.seedCorrection(t, "api", "oauth", "oauth retry c", s3)

	promoted, deferred, err := f.ds.promoteSkillsFromCorrections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if promoted != 1 || deferred != 0 {
		t.Errorf("want 1 promoted / 0 deferred, got %d / %d", promoted, deferred)
	}
	if n := f.skillCount(t); n != 1 {
		t.Errorf("want 1 skill, got %d", n)
	}
}

func TestGateAdmitsFailedSessionOrigin(t *testing.T) {
	f := newGateFixture(t)
	ctx := context.Background()

	failedSess := f.openSession(t, "api")
	f.seedCorrection(t, "api", "oauth", "oauth retry a", failedSess)
	if err := f.sess.Close(ctx, session.CloseInput{
		ID: failedSess, Summary: "blew up", Status: session.StatusFailed,
	}); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"oauth retry b", "oauth retry c"} {
		sessID := f.openSession(t, "api")
		f.seedCorrection(t, "api", "oauth", title, sessID)
	}

	promoted, deferred, err := f.ds.promoteSkillsFromCorrections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if promoted != 1 || deferred != 0 {
		t.Errorf("want 1 promoted / 0 deferred, got %d / %d", promoted, deferred)
	}
}

func TestGateAdmitsOverwhelmingFrequency(t *testing.T) {
	f := newGateFixture(t)
	ctx := context.Background()

	// Five corrections across two ok sessions, no injections, no failures:
	// frequency alone clears the gate at overwhelmingGroupSize.
	s1 := f.openSession(t, "api")
	s2 := f.openSession(t, "api")
	for i, title := range []string{"oauth a", "oauth b", "oauth c", "oauth d", "oauth e"} {
		sessID := s1
		if i%2 == 1 {
			sessID = s2
		}
		f.seedCorrection(t, "api", "oauth", title, sessID)
	}

	promoted, deferred, err := f.ds.promoteSkillsFromCorrections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if promoted != 1 || deferred != 0 {
		t.Errorf("want 1 promoted / 0 deferred, got %d / %d", promoted, deferred)
	}
}

func TestGateBypassedForExistingSkillUpdates(t *testing.T) {
	f := newGateFixture(t)
	ctx := context.Background()

	// Admit the skill the hard way first (failed-session evidence).
	failedSess := f.openSession(t, "api")
	f.seedCorrection(t, "api", "oauth", "oauth retry a", failedSess)
	_ = f.sess.Close(ctx, session.CloseInput{ID: failedSess, Summary: "x", Status: session.StatusFailed})
	for _, title := range []string{"oauth retry b", "oauth retry c"} {
		f.seedCorrection(t, "api", "oauth", title, f.openSession(t, "api"))
	}
	promoted, _, err := f.ds.promoteSkillsFromCorrections(ctx)
	if err != nil || promoted != 1 {
		t.Fatalf("setup promotion failed: promoted=%d err=%v", promoted, err)
	}

	// A new correction arrives with zero fresh outcome evidence. The
	// skill already exists, so this is maintenance: the version bumps
	// without re-passing the gate.
	f.seedCorrection(t, "api", "oauth", "oauth retry d", f.openSession(t, "api"))
	promoted, deferred, err := f.ds.promoteSkillsFromCorrections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if promoted != 1 || deferred != 0 {
		t.Errorf("existing skill update must bypass the gate: want 1 promoted / 0 deferred, got %d / %d", promoted, deferred)
	}
	list, _ := f.sk.List(ctx, "")
	if len(list) != 1 {
		t.Fatalf("want exactly 1 skill after update, got %d", len(list))
	}
	if list[0].Version < 2 {
		t.Errorf("skill version should have bumped, got %d", list[0].Version)
	}
}

func TestGateLegacyFallbackWithoutOutcomeStores(t *testing.T) {
	// promoteFixture (no Sessions/Injections wired) must keep the old
	// frequency-only behavior even for a single-session burst.
	f := newPromoteFixture(t)
	ctx := context.Background()

	// All three corrections share one session via direct saves.
	sess, err := f.sess.Open(ctx, session.OpenInput{Project: "api"})
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"oauth a", "oauth b", "oauth c"} {
		structured, _ := json.Marshal(correctionData{Tried: "x", WrongBecause: "y", Fix: "z"})
		if _, err := f.mem.Save(ctx, memory.SaveInput{
			Title: title, Content: "c", Type: memory.TypeCorrection,
			Project: "api", Tags: []string{"oauth"}, SessionID: sess.ID,
			Structured: string(structured),
		}); err != nil {
			t.Fatal(err)
		}
	}

	promoted, deferred, err := f.ds.promoteSkillsFromCorrections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if promoted != 1 || deferred != 0 {
		t.Errorf("legacy path must promote on frequency alone: want 1/0, got %d/%d", promoted, deferred)
	}
}
