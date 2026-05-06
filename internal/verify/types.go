// Package verify exercises the memory store from two angles:
//
//   - retrieval probes: prove a memory is findable when its trigger context
//     fires (cheap; pure search).
//   - behavior replay: prove the agent actually changes conduct with mnemos
//     enabled vs disabled (expensive; runs claude -p).
//
// Retrieval is the necessary precondition; behavior is the sufficient one.
// They report two independent numbers: precision@K and behavioral lift.
package verify

// RetrievalFixture is the YAML schema for retrieval probes.
type RetrievalFixture struct {
	Probes []RetrievalProbe `yaml:"probes"`
}

// RetrievalProbe asserts that a specific memory ID surfaces in the top
// ExpectInTop hits for at least one of the listed Queries.
type RetrievalProbe struct {
	ID          string   `yaml:"id"`
	Title       string   `yaml:"title,omitempty"`
	Queries     []string `yaml:"queries"`
	ExpectInTop int      `yaml:"expect_in_top"`
	Project     string   `yaml:"project,omitempty"`
}

// BehaviorFixture is the YAML schema for behavior replay scenarios. Arms
// hold the on/off command templates so the harness stays dumb — the
// fixture decides what "with mnemos" and "without mnemos" mean.
type BehaviorFixture struct {
	Arms      BehaviorArms       `yaml:"arms"`
	Scenarios []BehaviorScenario `yaml:"scenarios"`
}

// BehaviorArms supplies the two command templates compared per scenario.
// Each Cmd may contain {{trigger}}, replaced per run with the scenario's
// trigger prompt.
type BehaviorArms struct {
	On  ArmCmd `yaml:"on"`
	Off ArmCmd `yaml:"off"`
}

// ArmCmd is a single command template.
type ArmCmd struct {
	Cmd []string `yaml:"cmd"`
}

// BehaviorScenario asserts that running Trigger under the on-arm yields
// transcripts matching PassWhen at a higher rate than the off-arm. Runs
// controls the sample size per arm.
type BehaviorScenario struct {
	Name     string         `yaml:"name"`
	MemoryID string         `yaml:"memory_id"`
	Trigger  string         `yaml:"trigger"`
	PassWhen Assertions     `yaml:"pass_when"`
	Runs     int            `yaml:"runs"`
}

// Assertions holds substring-level checks against a transcript. ContainsAny
// passes when at least one substring is present; LacksAll passes when none
// are. Combine for "must mention X but not Y" patterns.
type Assertions struct {
	ContainsAny []string `yaml:"contains_any,omitempty"`
	LacksAll    []string `yaml:"lacks_all,omitempty"`
}
