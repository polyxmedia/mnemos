package skills_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/skills"
)

func TestBuildAndRoundTripPack(t *testing.T) {
	src := []skills.Skill{
		{
			Name:        "wire-mcp-tool",
			Description: "add a new MCP tool",
			Procedure:   "1. define\n2. register\n3. test",
			Pitfalls:    "forget the schema tag",
			Tags:        []string{"mcp", "go"},
			// Runtime fields that MUST NOT leak into the pack:
			UseCount:      99,
			Effectiveness: 0.42,
		},
	}

	pack := skills.BuildPack(skills.PackSource{Name: "@voidmode"}, src)
	buf, err := pack.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	// Confirm runtime fields aren't serialised.
	text := string(buf)
	for _, bad := range []string{`"use_count"`, `"effectiveness"`, `"version":0`} {
		if strings.Contains(text, bad) {
			t.Errorf("pack leaked field %q: %s", bad, text)
		}
	}

	// Round-trip.
	got, err := skills.UnmarshalPack(bytes.NewReader(buf))
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != skills.PackVersion {
		t.Errorf("version lost: %d", got.Version)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "wire-mcp-tool" {
		t.Errorf("skill lost: %+v", got.Skills)
	}
	if got.Source.Name != "@voidmode" {
		t.Errorf("source lost: %+v", got.Source)
	}
}

func TestUnmarshalRejectsFutureVersion(t *testing.T) {
	raw := `{"version": 9999, "skills": [{"name":"x","procedure":"y"}]}`
	_, err := skills.UnmarshalPack(strings.NewReader(raw))
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("expected newer-version error, got %v", err)
	}
}

func TestUnmarshalRejectsMissingVersion(t *testing.T) {
	raw := `{"skills": [{"name":"x","procedure":"y"}]}`
	if _, err := skills.UnmarshalPack(strings.NewReader(raw)); err == nil {
		t.Error("missing version must be rejected")
	}
}

func TestUnmarshalRejectsEmptySkills(t *testing.T) {
	raw := `{"version": 1, "skills": []}`
	if _, err := skills.UnmarshalPack(strings.NewReader(raw)); err == nil {
		t.Error("empty skills must be rejected")
	}
}

func TestUnmarshalRejectsEmptySkillFields(t *testing.T) {
	cases := []string{
		`{"version":1,"skills":[{"name":"","procedure":"p"}]}`,
		`{"version":1,"skills":[{"name":"n","procedure":""}]}`,
	}
	for _, raw := range cases {
		if _, err := skills.UnmarshalPack(strings.NewReader(raw)); err == nil {
			t.Errorf("invalid skill passed validation: %s", raw)
		}
	}
}

func TestUnmarshalRejectsUnknownFields(t *testing.T) {
	raw := `{"version":1,"skills":[{"name":"x","procedure":"y","stolen":"data"}]}`
	if _, err := skills.UnmarshalPack(strings.NewReader(raw)); err == nil {
		t.Error("unknown fields must be rejected (prevents typos + provides some hardening)")
	}
}
