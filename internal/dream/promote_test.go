package dream

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// ---- pure helper tests -----------------------------------------------------

func TestCorrectionLabel(t *testing.T) {
	cases := []struct {
		name  string
		obs   memory.Observation
		label string
	}{
		{
			name:  "first tag wins",
			obs:   memory.Observation{Title: "oauth retry without backoff", Tags: []string{"oauth", "retry"}},
			label: "oauth",
		},
		{
			name:  "structural tags ignored",
			obs:   memory.Observation{Title: "oauth retry", Tags: []string{tagPromoted, originPrefix + "abc", "oauth"}},
			label: "oauth",
		},
		{
			name:  "title fallback when no tags",
			obs:   memory.Observation{Title: "OAuth Retry Without Backoff"},
			label: "oauth retry without",
		},
		{
			name:  "empty title returns empty",
			obs:   memory.Observation{Title: ""},
			label: "",
		},
		{
			name:  "short title uses all words",
			obs:   memory.Observation{Title: "two words"},
			label: "two words",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := correctionLabel(c.obs)
			if got != c.label {
				t.Errorf("got %q want %q", got, c.label)
			}
		})
	}
}

func TestGroupHashIsStable(t *testing.T) {
	h1 := groupHash("agent", "proj", "oauth")
	h2 := groupHash("agent", "proj", "oauth")
	h3 := groupHash("agent", "proj", "auth")
	if h1 != h2 {
		t.Error("same inputs must produce same hash")
	}
	if h1 == h3 {
		t.Error("different labels must produce different hashes")
	}
	if len(h1) != 12 {
		t.Errorf("hash length %d, want 12", len(h1))
	}
}

func TestGroupCorrectionsDeterministic(t *testing.T) {
	// Two projects × two labels each × 3 corrections apiece. The output
	// slice must be sorted so repeated runs stay consistent.
	obs := []memory.Observation{
		{AgentID: "a", Project: "beta", Title: "json parse", Tags: []string{"json"}},
		{AgentID: "a", Project: "alpha", Title: "oauth retry", Tags: []string{"oauth"}},
		{AgentID: "a", Project: "beta", Title: "json parse", Tags: []string{"json"}},
		{AgentID: "a", Project: "alpha", Title: "rate limit", Tags: []string{"rate-limit"}},
		{AgentID: "a", Project: "alpha", Title: "oauth retry", Tags: []string{"oauth"}},
		{AgentID: "a", Project: "alpha", Title: "rate limit", Tags: []string{"rate-limit"}},
	}
	groups := groupCorrections(obs)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	wantOrder := []struct{ project, label string }{
		{"alpha", "oauth"},
		{"alpha", "rate-limit"},
		{"beta", "json"},
	}
	for i, w := range wantOrder {
		if groups[i].project != w.project || groups[i].label != w.label {
			t.Errorf("group[%d] = (%s, %s), want (%s, %s)",
				i, groups[i].project, groups[i].label, w.project, w.label)
		}
	}
}

func TestGroupCorrectionsSkipsUnlabelable(t *testing.T) {
	obs := []memory.Observation{
		{AgentID: "a", Project: "p", Title: "", Tags: nil},
		{AgentID: "a", Project: "p", Title: "oauth", Tags: []string{"oauth"}},
	}
	groups := groupCorrections(obs)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group (unlabelable skipped), got %d", len(groups))
	}
}

func TestSynthesisePromotionAssemblesSections(t *testing.T) {
	c := correctionData{
		Tried:          "retry on 401",
		WrongBecause:   "401 is auth failure not transient",
		Fix:            "refresh token then retry once",
		TriggerContext: "oauth 401 during api call",
	}
	raw, _ := json.Marshal(c)
	g := correctionGroup{
		agentID: "a", project: "api", label: "oauth",
		corrections: []memory.Observation{
			{Title: "oauth retry 1", Structured: string(raw)},
			{Title: "oauth retry 2", Structured: string(raw)},
			{Title: "oauth retry 3", Structured: string(raw)},
		},
	}
	proc, pitfalls := synthesisePromotion(g)
	for _, want := range []string{"## When this applies", "oauth 401 during api call",
		"## Avoid", "retry on 401", "## Do", "refresh token"} {
		if !strings.Contains(proc, want) {
			t.Errorf("procedure missing %q:\n%s", want, proc)
		}
	}
	if !strings.Contains(pitfalls, "401 is auth failure") {
		t.Errorf("pitfalls missing wrong_because: %s", pitfalls)
	}
	// Identical corrections collapse in Avoid/Do/Pitfalls to avoid verbatim
	// repetition — stable-dedupe across the 3 copies above.
	if strings.Count(proc, "- retry on 401") != 1 {
		t.Errorf("Avoid section should dedupe identical entries:\n%s", proc)
	}
}

func TestSameSourceSetIgnoresOrder(t *testing.T) {
	if !sameSourceSet([]string{"a", "b"}, []string{"b", "a"}) {
		t.Error("unordered equal sets should match")
	}
	if sameSourceSet([]string{"a"}, []string{"a", "b"}) {
		t.Error("different sizes should not match")
	}
	if sameSourceSet([]string{"a", "b"}, []string{"a", "c"}) {
		t.Error("different members should not match")
	}
}

// ---- integration tests -----------------------------------------------------

type promoteFixture struct {
	ds   *Service
	mem  *memory.Service
	sess *session.Service
	sk   *skills.Service
}

func newPromoteFixture(t *testing.T) *promoteFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mem := memory.NewService(memory.Config{Store: db.Observations()})
	sess := session.NewService(session.Config{Store: db.Sessions()})
	sk := skills.NewService(skills.Config{Store: db.Skills()})
	ds := NewService(Config{
		Memory:    mem,
		Store:     db.Observations(),
		Reader:    db.Observations(),
		Skills:    sk,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		StaleDays: 1,
	})
	return &promoteFixture{ds: ds, mem: mem, sess: sess, sk: sk}
}

// seedCorrection opens a session (to satisfy the FK on observations.session_id)
// and writes a correction bound to it. Returns the session_id so tests can
// assert on provenance — without that surface the "source sessions merged"
// behaviour cannot be verified.
func (f *promoteFixture) seedCorrection(t *testing.T, project, tag, title string) string {
	t.Helper()
	sess, err := f.sess.Open(context.Background(), session.OpenInput{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	c := correctionData{
		Tried:          "tried " + title,
		WrongBecause:   "because " + title,
		Fix:            "fix for " + title,
		TriggerContext: "trigger for " + tag,
	}
	raw, _ := json.Marshal(c)
	_, err = f.mem.Save(context.Background(), memory.SaveInput{
		Title: title, Content: title, Type: memory.TypeCorrection,
		Tags: []string{tag}, Project: project, SessionID: sess.ID,
		Structured: string(raw), Importance: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sess.ID
}

func TestPromoteBelowThresholdIsNoOp(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	// Only 2 corrections in a group — threshold is 3.
	f.seedCorrection(t, "api", "oauth", "oauth a")
	f.seedCorrection(t, "api", "oauth", "oauth b")

	j, err := f.ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.Promoted != 0 {
		t.Errorf("expected 0 promotions below threshold, got %d", j.Promoted)
	}
	list, _ := f.sk.List(ctx, "")
	if len(list) != 0 {
		t.Errorf("no skills should exist, got %d", len(list))
	}
}

func TestPromoteAtThresholdCreatesSkill(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		f.seedCorrection(t, "api", "oauth",
			"oauth retry "+string(rune('a'+i)))
	}

	j, err := f.ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.Promoted != 1 {
		t.Fatalf("expected 1 promotion, got %d", j.Promoted)
	}

	list, _ := f.sk.List(ctx, "")
	if len(list) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(list))
	}
	skill := list[0]
	if !strings.Contains(skill.Name, "oauth") || !strings.Contains(skill.Name, "api") {
		t.Errorf("skill name should embed label + project: %q", skill.Name)
	}
	if !strings.Contains(skill.Procedure, "## Avoid") {
		t.Errorf("procedure missing Avoid section: %s", skill.Procedure)
	}
	tags := skill.Tags
	hasPromoted := false
	hasOrigin := false
	for _, tag := range tags {
		if tag == tagPromoted {
			hasPromoted = true
		}
		if strings.HasPrefix(tag, originPrefix) {
			hasOrigin = true
		}
	}
	if !hasPromoted {
		t.Errorf("skill missing %q tag", tagPromoted)
	}
	if !hasOrigin {
		t.Errorf("skill missing origin tag")
	}
	// Source sessions must survive — they're the provenance for the demo
	// ("this skill came from sessions X, Y, Z").
	if len(skill.SourceSessions) != 3 {
		t.Errorf("expected 3 source_sessions, got %d", len(skill.SourceSessions))
	}
}

func TestPromoteIsIdempotent(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		f.seedCorrection(t, "api", "oauth",
			"oauth retry "+string(rune('a'+i)))
	}

	// First pass promotes.
	if _, err := f.ds.Run(ctx, false); err != nil {
		t.Fatal(err)
	}
	list, _ := f.sk.List(ctx, "")
	versionOne := list[0].Version

	// Second pass: same corpus. Must not create a duplicate or bump
	// version; nothing changed since the first run.
	j, err := f.ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.Promoted != 0 {
		t.Errorf("rerun should be a no-op, got %d promotions", j.Promoted)
	}
	list2, _ := f.sk.List(ctx, "")
	if len(list2) != 1 {
		t.Errorf("should still be 1 skill, got %d", len(list2))
	}
	if list2[0].Version != versionOne {
		t.Errorf("version should not bump on no-op rerun: was %d, now %d",
			versionOne, list2[0].Version)
	}
}

func TestPromoteVersionBumpsWhenNewCorrectionsJoin(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		f.seedCorrection(t, "api", "oauth",
			"oauth retry "+string(rune('a'+i)))
	}
	_, _ = f.ds.Run(ctx, false)
	list, _ := f.sk.List(ctx, "")
	versionOne := list[0].Version

	// A fourth correction joins the same group. Next pass must bump the
	// existing skill rather than creating a new one.
	f.seedCorrection(t, "api", "oauth", "oauth retry d")

	j, err := f.ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.Promoted != 1 {
		t.Errorf("expected 1 promotion (version bump), got %d", j.Promoted)
	}
	list2, _ := f.sk.List(ctx, "")
	if len(list2) != 1 {
		t.Errorf("still 1 skill, got %d", len(list2))
	}
	if list2[0].Version != versionOne+1 {
		t.Errorf("expected version %d, got %d", versionOne+1, list2[0].Version)
	}
	if len(list2[0].SourceSessions) != 4 {
		t.Errorf("expected 4 source_sessions after merge, got %d",
			len(list2[0].SourceSessions))
	}
}

func TestPromoteDisabledWhenReaderMissing(t *testing.T) {
	// Wiring dream without Reader/Skills must keep working: the rest of
	// the pass runs, promotion silently skips. This is the compat path
	// for any caller who constructs dream with the old Config shape.
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mem := memory.NewService(memory.Config{Store: db.Observations()})
	ds := NewService(Config{
		Memory:    mem,
		Store:     db.Observations(),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		StaleDays: 1,
	})
	j, err := ds.Run(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if j.Promoted != 0 {
		t.Errorf("no reader ⇒ no promotions, got %d", j.Promoted)
	}
}

func TestPromoteHandlesMultipleGroups(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	// Three corrections for oauth, three for serialisation → two groups.
	for i := 0; i < 3; i++ {
		f.seedCorrection(t, "api", "oauth",
			"oauth x"+string(rune('a'+i)))
	}
	for i := 0; i < 3; i++ {
		f.seedCorrection(t, "api", "serialisation",
			"ser y"+string(rune('a'+i)))
	}
	j, err := f.ds.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.Promoted != 2 {
		t.Errorf("expected 2 promotions for 2 groups, got %d", j.Promoted)
	}
	list, _ := f.sk.List(ctx, "")
	if len(list) != 2 {
		t.Errorf("expected 2 skills, got %d", len(list))
	}
}
