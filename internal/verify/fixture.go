package verify

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadRetrievalFixture parses a YAML file describing retrieval probes.
func LoadRetrievalFixture(path string) (*RetrievalFixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f RetrievalFixture
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i, p := range f.Probes {
		if p.ID == "" {
			return nil, fmt.Errorf("probe %d: missing id", i)
		}
		if len(p.Queries) == 0 {
			return nil, fmt.Errorf("probe %s: at least one query required", p.ID)
		}
		if p.ExpectInTop <= 0 {
			f.Probes[i].ExpectInTop = 5
		}
	}
	return &f, nil
}

// LoadCaptureFixture parses a YAML file describing write-side capture
// scenarios. Single-arm by design: the off-arm trivially fails (no tools
// to call) so a paired comparison is uninformative; the metric that
// matters is the on-arm capture rate per scenario.
func LoadCaptureFixture(path string) (*CaptureFixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f CaptureFixture
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(f.Arm.Cmd) == 0 {
		return nil, fmt.Errorf("arm.cmd is required")
	}
	for i, s := range f.Scenarios {
		if s.Name == "" {
			return nil, fmt.Errorf("scenario %d: missing name", i)
		}
		if s.Trigger == "" {
			return nil, fmt.Errorf("scenario %s: missing trigger", s.Name)
		}
		if len(s.ExpectTools) == 0 {
			return nil, fmt.Errorf("scenario %s: expect_tools required", s.Name)
		}
		if s.Runs <= 0 {
			f.Scenarios[i].Runs = 3
		}
	}
	return &f, nil
}

// LoadBehaviorFixture parses a YAML file describing behavior scenarios.
func LoadBehaviorFixture(path string) (*BehaviorFixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f BehaviorFixture
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(f.Arms.On.Cmd) == 0 || len(f.Arms.Off.Cmd) == 0 {
		return nil, fmt.Errorf("both arms.on.cmd and arms.off.cmd are required")
	}
	for i, s := range f.Scenarios {
		if s.Name == "" {
			return nil, fmt.Errorf("scenario %d: missing name", i)
		}
		if s.Trigger == "" {
			return nil, fmt.Errorf("scenario %s: missing trigger", s.Name)
		}
		if s.Runs <= 0 {
			f.Scenarios[i].Runs = 3
		}
	}
	return &f, nil
}
