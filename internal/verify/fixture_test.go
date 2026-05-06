package verify

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadRetrievalFixtureDefaultsExpectInTop(t *testing.T) {
	p := writeTemp(t, "r.yaml", `
probes:
  - id: ABC
    queries: ["x"]
`)
	f, err := LoadRetrievalFixture(p)
	if err != nil {
		t.Fatal(err)
	}
	if f.Probes[0].ExpectInTop != 5 {
		t.Errorf("default expect_in_top = %d, want 5", f.Probes[0].ExpectInTop)
	}
}

func TestLoadRetrievalFixtureRejectsEmptyQueries(t *testing.T) {
	p := writeTemp(t, "r.yaml", `
probes:
  - id: ABC
`)
	if _, err := LoadRetrievalFixture(p); err == nil {
		t.Error("expected error for missing queries")
	}
}

func TestLoadBehaviorFixtureRequiresArms(t *testing.T) {
	p := writeTemp(t, "b.yaml", `
scenarios:
  - name: s1
    trigger: t
`)
	if _, err := LoadBehaviorFixture(p); err == nil {
		t.Error("expected error for missing arms")
	}
}

func TestLoadBehaviorFixtureDefaultsRuns(t *testing.T) {
	p := writeTemp(t, "b.yaml", `
arms:
  on:
    cmd: ["echo", "on"]
  off:
    cmd: ["echo", "off"]
scenarios:
  - name: s1
    trigger: t
    pass_when:
      contains_any: ["x"]
`)
	f, err := LoadBehaviorFixture(p)
	if err != nil {
		t.Fatal(err)
	}
	if f.Scenarios[0].Runs != 3 {
		t.Errorf("default runs = %d, want 3", f.Scenarios[0].Runs)
	}
}
