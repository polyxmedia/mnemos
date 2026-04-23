package rumination

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// fakeSkillReader satisfies SkillReader for service tests without pulling
// in the full skills.Store surface.
type fakeSkillReader struct {
	items []skills.Skill
	err   error
}

func (f *fakeSkillReader) Get(ctx context.Context, id string) (*skills.Skill, error) {
	if f.err != nil {
		return nil, f.err
	}
	for i := range f.items {
		if f.items[i].ID == id {
			copy := f.items[i]
			return &copy, nil
		}
	}
	return nil, skills.ErrNotFound
}

func (f *fakeSkillReader) List(ctx context.Context, agentID string) ([]skills.Skill, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

// fakeMemoryReader is a minimal memory.Reader that satisfies the service's
// TargetObservation resolution path. Only Get is exercised here; the
// other methods exist so the interface is satisfied.
type fakeMemoryReader struct {
	items map[string]*memory.Observation
}

func (f *fakeMemoryReader) Get(ctx context.Context, id string) (*memory.Observation, error) {
	o, ok := f.items[id]
	if !ok {
		return nil, memory.ErrNotFound
	}
	return o, nil
}

func (f *fakeMemoryReader) Search(ctx context.Context, in memory.SearchInput) ([]memory.SearchResult, error) {
	return nil, nil
}
func (f *fakeMemoryReader) ListByProject(ctx context.Context, agentID, project string, t memory.ObsType, limit int) ([]memory.Observation, error) {
	return nil, nil
}
func (f *fakeMemoryReader) ListBySession(ctx context.Context, sessionID string) ([]memory.Observation, error) {
	return nil, nil
}
func (f *fakeMemoryReader) ListByTitleSimilarity(ctx context.Context, agentID, title string, limit int) ([]memory.Observation, error) {
	return nil, nil
}
func (f *fakeMemoryReader) FindByContentHash(ctx context.Context, agentID, project, hash string) (*memory.Observation, error) {
	return nil, nil
}
func (f *fakeMemoryReader) ListLinks(ctx context.Context, linkType memory.LinkType, agentID string, limit int) ([]memory.LinkEdge, error) {
	return nil, nil
}
func (f *fakeMemoryReader) ListByTrustTier(ctx context.Context, agentID string, tier memory.TrustTier, limit int) ([]memory.Observation, error) {
	return nil, nil
}
func (f *fakeMemoryReader) Stats(ctx context.Context) (memory.Stats, error) {
	return memory.Stats{}, nil
}

func TestService_Detect_SortsBySeverity(t *testing.T) {
	reader := &fakeSkillReader{items: []skills.Skill{
		{ID: "s-low", Name: "low", UseCount: 15, SuccessCount: 4, Effectiveness: 0.27},
		{ID: "s-high", Name: "high", UseCount: 20, SuccessCount: 2, Effectiveness: 0.10},
		{ID: "s-mid", Name: "mid", UseCount: 20, SuccessCount: 4, Effectiveness: 0.20},
	}}
	svc := NewService(Config{
		Monitors: []Monitor{&SkillEffectivenessMonitor{Skills: reader}},
		Skills:   reader,
	})

	got, err := svc.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 candidates, got %d", len(got))
	}
	// Severity should float the worst skill to the top.
	if got[0].TargetID != "s-high" {
		t.Errorf("top candidate should be s-high, got %s", got[0].TargetID)
	}
	if got[1].Severity < got[2].Severity {
		t.Errorf("candidates not severity-descending: %+v", got)
	}
}

func TestService_Detect_MonitorError(t *testing.T) {
	want := errors.New("kaboom")
	svc := NewService(Config{
		Monitors: []Monitor{&SkillEffectivenessMonitor{Skills: &fakeSkillLister{err: want}}},
	})
	_, err := svc.Detect(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want wrapping of %v", err, want)
	}
	// Monitor name should also appear in the wrap so logs are readable.
	if !strings.Contains(err.Error(), "skill-effectiveness-floor") {
		t.Errorf("err missing monitor name context: %v", err)
	}
}

func TestService_Pack_SkillTarget(t *testing.T) {
	reader := &fakeSkillReader{items: []skills.Skill{{
		ID: "sk1", Name: "retry on 401", Procedure: "retry the request",
		UseCount: 12, SuccessCount: 3, Effectiveness: 0.25,
	}}}
	svc := NewService(Config{Skills: reader})

	c := Candidate{
		ID:          "rumination-x",
		MonitorName: "skill-effectiveness-floor",
		Severity:    SeverityMedium,
		Reason:      "effectiveness 0.25",
		TargetKind:  TargetSkill,
		TargetID:    "sk1",
		Evidence:    []Evidence{{Label: "history", Content: "3/12"}},
	}
	block, err := svc.Pack(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if block.Target.Name != "retry on 401" {
		t.Errorf("target name = %s", block.Target.Name)
	}
	if !strings.Contains(block.Text, "retry the request") {
		t.Errorf("block should embed target body verbatim:\n%s", block.Text)
	}
}

func TestService_Pack_ObservationTarget(t *testing.T) {
	obs := &memory.Observation{
		ID: "o1", Title: "always wrap errors", Content: "use fmt.Errorf with %w",
	}
	mem := &fakeMemoryReader{items: map[string]*memory.Observation{"o1": obs}}
	svc := NewService(Config{Memory: mem})

	c := Candidate{
		ID:         "rumination-o",
		TargetKind: TargetObservation,
		TargetID:   "o1",
	}
	block, err := svc.Pack(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if block.Target.Body != "use fmt.Errorf with %w" {
		t.Errorf("observation body not resolved")
	}
	if !strings.Contains(block.Text, "mnemos_save") {
		t.Errorf("action section should point at mnemos_save")
	}
}

func TestService_Pack_MissingStoreErrors(t *testing.T) {
	svc := NewService(Config{})
	_, err := svc.Pack(context.Background(), Candidate{TargetKind: TargetSkill, TargetID: "x"})
	if err == nil || !strings.Contains(err.Error(), "skills reader not configured") {
		t.Errorf("missing skills store should error clearly, got %v", err)
	}
	_, err = svc.Pack(context.Background(), Candidate{TargetKind: TargetObservation, TargetID: "x"})
	if err == nil || !strings.Contains(err.Error(), "memory reader not configured") {
		t.Errorf("missing memory reader should error clearly, got %v", err)
	}
}

func TestService_Pack_UnknownKind(t *testing.T) {
	svc := NewService(Config{})
	_, err := svc.Pack(context.Background(), Candidate{TargetKind: "bogus", TargetID: "x"})
	if err == nil || !strings.Contains(err.Error(), "unknown target kind") {
		t.Errorf("unknown kind should error clearly, got %v", err)
	}
}
