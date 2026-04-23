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
