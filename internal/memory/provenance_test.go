package memory_test

import (
	"context"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
)

func TestSaveDefaultsProvenanceFields(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	res, err := svc.Save(ctx, memory.SaveInput{
		Title:   "x",
		Content: "y",
		Type:    memory.TypeDecision,
		Project: "p",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if res.Observation.SourceKind != memory.SourceUser {
		t.Errorf("default SourceKind must be SourceUser, got %q", res.Observation.SourceKind)
	}
	if res.Observation.TrustTier != memory.TrustCurated {
		t.Errorf("default TrustTier must be TrustCurated, got %q", res.Observation.TrustTier)
	}
	if len(res.Observation.DerivedFrom) != 0 {
		t.Errorf("default DerivedFrom must be empty, got %v", res.Observation.DerivedFrom)
	}
}

func TestSavePreservesExplicitProvenance(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	parent, err := svc.Save(ctx, memory.SaveInput{
		Title: "parent", Content: "p", Type: memory.TypeDecision, Project: "p",
	})
	if err != nil {
		t.Fatalf("save parent: %v", err)
	}

	res, err := svc.Save(ctx, memory.SaveInput{
		Title:       "tool fetch",
		Content:     "random noise from a web page",
		Type:        memory.TypeContext,
		Project:     "p",
		SourceKind:  memory.SourceTool,
		TrustTier:   memory.TrustRaw,
		DerivedFrom: []string{parent.Observation.ID},
	})
	if err != nil {
		t.Fatalf("save tool-derived: %v", err)
	}
	if res.Observation.SourceKind != memory.SourceTool {
		t.Errorf("explicit SourceKind lost: got %q", res.Observation.SourceKind)
	}
	if res.Observation.TrustTier != memory.TrustRaw {
		t.Errorf("explicit TrustTier lost: got %q", res.Observation.TrustTier)
	}
	if len(res.Observation.DerivedFrom) != 1 || res.Observation.DerivedFrom[0] != parent.Observation.ID {
		t.Errorf("explicit DerivedFrom lost: got %v", res.Observation.DerivedFrom)
	}
}

func TestSaveRejectsInvalidSourceKind(t *testing.T) {
	svc := newService(t, nil)
	_, err := svc.Save(context.Background(), memory.SaveInput{
		Title: "x", Content: "y", Type: memory.TypeDecision, Project: "p",
		SourceKind: memory.SourceKind("garbage"),
	})
	if err == nil {
		t.Fatal("invalid source_kind must error")
	}
}

func TestSaveRejectsInvalidTrustTier(t *testing.T) {
	svc := newService(t, nil)
	_, err := svc.Save(context.Background(), memory.SaveInput{
		Title: "x", Content: "y", Type: memory.TypeDecision, Project: "p",
		TrustTier: memory.TrustTier("nope"),
	})
	if err == nil {
		t.Fatal("invalid trust_tier must error")
	}
}

func TestSourceKindValid(t *testing.T) {
	cases := map[memory.SourceKind]bool{
		memory.SourceUser:           true,
		memory.SourceTool:           true,
		memory.SourceAgentInference: true,
		memory.SourceDream:          true,
		memory.SourceImport:         true,
		"":                          false,
		"totally-made-up":           false,
	}
	for sk, want := range cases {
		if got := sk.Valid(); got != want {
			t.Errorf("SourceKind(%q).Valid() = %v, want %v", sk, got, want)
		}
	}
}

func TestTrustTierValid(t *testing.T) {
	cases := map[memory.TrustTier]bool{
		memory.TrustRaw:     true,
		memory.TrustCurated: true,
		memory.TrustSkill:   true,
		"":                  false,
		"archived":          false,
	}
	for tt, want := range cases {
		if got := tt.Valid(); got != want {
			t.Errorf("TrustTier(%q).Valid() = %v, want %v", tt, got, want)
		}
	}
}

func TestRawTierExcludedFromDefaultSearch(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	if _, err := svc.Save(ctx, memory.SaveInput{
		Title: "curated fact", Content: "sqlite is pure-Go",
		Type: memory.TypeDecision, Project: "p",
	}); err != nil {
		t.Fatalf("save curated: %v", err)
	}
	if _, err := svc.Save(ctx, memory.SaveInput{
		Title: "tool-fetched", Content: "sqlite is pure-Go magic",
		Type: memory.TypeContext, Project: "p",
		SourceKind: memory.SourceTool, TrustTier: memory.TrustRaw,
	}); err != nil {
		t.Fatalf("save raw: %v", err)
	}

	hits, err := svc.Search(ctx, memory.SearchInput{Query: "sqlite"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, h := range hits {
		if h.Observation.TrustTier == memory.TrustRaw {
			t.Errorf("default search must exclude raw-tier, found %s", h.Observation.ID)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 curated hit, got %d", len(hits))
	}

	hits, err = svc.Search(ctx, memory.SearchInput{Query: "sqlite", IncludeRaw: true})
	if err != nil {
		t.Fatalf("search with IncludeRaw: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("IncludeRaw must surface raw rows, got %d (want 2)", len(hits))
	}
}

func TestPromoteMovesTierWhenJustified(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	res, err := svc.Save(ctx, memory.SaveInput{
		Title: "raw", Content: "unverified tool output",
		Type: memory.TypeContext, Project: "p",
		SourceKind: memory.SourceTool, TrustTier: memory.TrustRaw,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	err = svc.Promote(ctx, memory.PromoteInput{
		ID:        res.Observation.ID,
		ToTier:    memory.TrustCurated,
		WhyBetter: "user confirmed the fact matches their current setup",
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}

	got, _ := svc.Get(ctx, res.Observation.ID)
	if got.TrustTier != memory.TrustCurated {
		t.Errorf("expected TrustCurated after promote, got %q", got.TrustTier)
	}
}

func TestPromoteRejectsThinReason(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()
	res, _ := svc.Save(ctx, memory.SaveInput{
		Title: "x", Content: "y", Type: memory.TypeContext, Project: "p",
		TrustTier: memory.TrustRaw,
	})

	cases := []struct {
		name string
		why  string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"too-short", "ok"},
		{"just-under-threshold", "fifteen char.."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := svc.Promote(ctx, memory.PromoteInput{
				ID: res.Observation.ID, ToTier: memory.TrustCurated, WhyBetter: c.why,
			})
			if err == nil {
				t.Errorf("promote must reject %q reason", c.why)
			}
		})
	}
}

func TestPromoteRejectsInvalidTier(t *testing.T) {
	svc := newService(t, nil)
	err := svc.Promote(context.Background(), memory.PromoteInput{
		ID: "anything", ToTier: memory.TrustTier("archived"),
		WhyBetter: "this is a valid-length reason for testing",
	})
	if err == nil {
		t.Error("invalid to_tier must error")
	}
}

func TestPromoteNotFoundIsSurfaced(t *testing.T) {
	svc := newService(t, nil)
	err := svc.Promote(context.Background(), memory.PromoteInput{
		ID: "nonexistent", ToTier: memory.TrustCurated,
		WhyBetter: "valid reason with enough characters",
	})
	if err == nil {
		t.Error("missing observation must error")
	}
}

func TestProvenanceRoundTripThroughStorage(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	res, err := svc.Save(ctx, memory.SaveInput{
		Title:       "quarantined",
		Content:     "raw tool output",
		Type:        memory.TypeContext,
		Project:     "p",
		SourceKind:  memory.SourceTool,
		TrustTier:   memory.TrustRaw,
		DerivedFrom: []string{"01KPXXX", "01KPYYY"},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := svc.Get(ctx, res.Observation.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SourceKind != memory.SourceTool {
		t.Errorf("SourceKind not persisted: got %q", got.SourceKind)
	}
	if got.TrustTier != memory.TrustRaw {
		t.Errorf("TrustTier not persisted: got %q", got.TrustTier)
	}
	if len(got.DerivedFrom) != 2 || got.DerivedFrom[0] != "01KPXXX" || got.DerivedFrom[1] != "01KPYYY" {
		t.Errorf("DerivedFrom not persisted: got %v", got.DerivedFrom)
	}
}
