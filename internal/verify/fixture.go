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
