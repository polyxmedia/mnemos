package verify

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Executor runs one command-arm invocation and returns the captured
// transcript (stdout — typically the JSON --output-format produces, but
// the harness only does substring matching so any format works). Declared
// at the consumer side so tests skip the real subprocess.
type Executor interface {
	Run(ctx context.Context, cmd []string) (string, error)
}

// ExecExecutor is the production executor: shells out to the configured
// command. Errors from the subprocess are surfaced as transcript content
// so a non-zero exit doesn't silently mask an assertion miss.
type ExecExecutor struct{}

// Run shells out via os/exec. Combined output is returned even on non-zero
// exit so assertions still see what the agent printed before failing.
func (ExecExecutor) Run(ctx context.Context, cmd []string) (string, error) {
	if len(cmd) == 0 {
		return "", fmt.Errorf("empty command")
	}
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	out, err := c.CombinedOutput()
	return string(out), err
}

// BehaviorReport summarises the A/B run.
type BehaviorReport struct {
	Scenarios []ScenarioOutcome
}

// ScenarioOutcome holds per-scenario A/B counts.
type ScenarioOutcome struct {
	Scenario BehaviorScenario
	OnPass   int
	OffPass  int
	Runs     int
	Errors   []string // any subprocess errors (still counted as fail)
}

// Lift returns pass_rate(on) - pass_rate(off) as a fraction in [-1, 1].
// Positive lift means mnemos changed behavior in the desired direction.
func (s ScenarioOutcome) Lift() float64 {
	if s.Runs == 0 {
		return 0
	}
	return float64(s.OnPass-s.OffPass) / float64(s.Runs)
}

// RunBehavior executes every scenario in both arms and tallies passes.
// Substitutes {{trigger}} in each command template. Stops only on context
// cancellation; subprocess errors per run are recorded but don't abort the
// run — a flaky single invocation shouldn't kill the whole report.
func RunBehavior(ctx context.Context, exe Executor, fix *BehaviorFixture) (*BehaviorReport, error) {
	if fix == nil {
		return nil, fmt.Errorf("nil fixture")
	}
	rep := &BehaviorReport{}
	for _, sc := range fix.Scenarios {
		out := ScenarioOutcome{Scenario: sc, Runs: sc.Runs}
		for i := 0; i < sc.Runs; i++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			onCmd := substitute(fix.Arms.On.Cmd, sc.Trigger)
			offCmd := substitute(fix.Arms.Off.Cmd, sc.Trigger)

			onTr, onErr := exe.Run(ctx, onCmd)
			if onErr != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("on run %d: %v", i+1, onErr))
			}
			if Evaluate(onTr, sc.PassWhen) {
				out.OnPass++
			}

			offTr, offErr := exe.Run(ctx, offCmd)
			if offErr != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("off run %d: %v", i+1, offErr))
			}
			if Evaluate(offTr, sc.PassWhen) {
				out.OffPass++
			}
		}
		rep.Scenarios = append(rep.Scenarios, out)
	}
	return rep, nil
}

// Evaluate applies the assertion bundle to a transcript. ContainsAny passes
// when at least one substring is found; LacksAll passes when none of the
// listed substrings appear. Both must hold when both lists are non-empty.
func Evaluate(transcript string, a Assertions) bool {
	if len(a.ContainsAny) > 0 {
		any := false
		for _, sub := range a.ContainsAny {
			if strings.Contains(transcript, sub) {
				any = true
				break
			}
		}
		if !any {
			return false
		}
	}
	for _, sub := range a.LacksAll {
		if strings.Contains(transcript, sub) {
			return false
		}
	}
	return true
}

// substitute swaps {{trigger}} in every cmd token. Done per token so a
// trigger containing spaces stays a single argv entry.
func substitute(cmd []string, trigger string) []string {
	out := make([]string, len(cmd))
	for i, tok := range cmd {
		out[i] = strings.ReplaceAll(tok, "{{trigger}}", trigger)
	}
	return out
}
