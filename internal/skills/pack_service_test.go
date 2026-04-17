package skills_test

import (
	"context"
	"testing"

	"github.com/polyxmedia/mnemos/internal/skills"
)

func TestExportPackAllSkills(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()
	for _, name := range []string{"a", "b", "c"} {
		if _, err := svc.Save(ctx, skills.SaveInput{
			Name: name, Description: "d", Procedure: "p",
		}); err != nil {
			t.Fatal(err)
		}
	}
	pack, err := svc.ExportPack(ctx, "", nil, skills.PackSource{Name: "@test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Skills) != 3 {
		t.Errorf("want 3 skills in pack, got %d", len(pack.Skills))
	}
	if pack.Source.Name != "@test" {
		t.Errorf("source not preserved: %+v", pack.Source)
	}
	if pack.Version != skills.PackVersion {
		t.Errorf("version not set")
	}
}

func TestExportPackSelectedNames(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()
	for _, name := range []string{"a", "b", "c"} {
		_, _ = svc.Save(ctx, skills.SaveInput{Name: name, Description: "d", Procedure: "p"})
	}
	pack, err := svc.ExportPack(ctx, "", []string{"a", "c"}, skills.PackSource{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Skills) != 2 {
		t.Errorf("want 2, got %d", len(pack.Skills))
	}
}

func TestExportPackUnknownNameErrors(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()
	_, _ = svc.Save(ctx, skills.SaveInput{Name: "known", Description: "d", Procedure: "p"})
	_, err := svc.ExportPack(ctx, "", []string{"missing"}, skills.PackSource{})
	if err == nil {
		t.Error("unknown skill name must error")
	}
}

func TestExportPackEmptyStoreErrors(t *testing.T) {
	svc := newSvc(t)
	_, err := svc.ExportPack(context.Background(), "", nil, skills.PackSource{})
	if err == nil {
		t.Error("empty store must error on export-all")
	}
}

func TestImportPackCreatesAndUpdates(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()

	// Seed one existing skill with the same name as the pack.
	_, _ = svc.Save(ctx, skills.SaveInput{
		Name: "existing", Description: "old", Procedure: "old-proc",
	})

	pack := &skills.Pack{
		Version: skills.PackVersion,
		Source:  skills.PackSource{Name: "@imported"},
		Skills: []skills.PackSkill{
			{Name: "existing", Description: "new", Procedure: "new-proc"},
			{Name: "new-skill", Description: "fresh", Procedure: "fresh-proc"},
		},
	}
	res, err := svc.ImportPack(ctx, "", pack)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 || res.Updated != 1 {
		t.Errorf("want 1 created + 1 updated, got %+v", res)
	}
	if res.Source.Name != "@imported" {
		t.Errorf("source not passed through: %+v", res.Source)
	}

	// Verify the existing skill was updated, not duplicated.
	list, _ := svc.List(ctx, "")
	if len(list) != 2 {
		t.Errorf("want 2 skills total after import, got %d", len(list))
	}
	for _, sk := range list {
		if sk.Name == "existing" && sk.Version < 2 {
			t.Errorf("existing skill should have version bumped, got v%d", sk.Version)
		}
	}
}
