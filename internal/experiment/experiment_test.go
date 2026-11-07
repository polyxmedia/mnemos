package experiment_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/experiment"
)

func line(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func userText(text string) map[string]any {
	return map[string]any{"type": "user", "message": map[string]any{"content": text}}
}

func assistant(blocks ...map[string]any) map[string]any {
	return map[string]any{"type": "assistant", "message": map[string]any{"content": blocks}}
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func tool(name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "name": name, "input": input}
}

func TestReadTranscriptCountsOnlyWhatItShould(t *testing.T) {
	var b strings.Builder
	b.WriteString(line(t, userText("add the export button")))
	b.WriteString(line(t, map[string]any{"type": "user", "message": map[string]any{
		"content": []map[string]any{{"type": "text", "text": "tried it again still not working"}},
	}}))
	// None of these are typed prompts.
	b.WriteString(line(t, map[string]any{"type": "user", "isMeta": true, "message": map[string]any{"content": "meta still"}}))
	b.WriteString(line(t, map[string]any{"type": "user", "isCompactSummary": true, "message": map[string]any{"content": "summary still"}}))
	b.WriteString(line(t, map[string]any{"type": "user", "isSidechain": true, "message": map[string]any{"content": "subagent still"}}))
	b.WriteString(line(t, userText("Caveat: local command output")))
	b.WriteString(line(t, userText("<synthetic> still")))
	b.WriteString(line(t, map[string]any{"type": "user", "message": map[string]any{
		"content": []map[string]any{{"type": "tool_result", "content": "still"}},
	}}))
	b.WriteString("this line is not json\n")

	b.WriteString(line(t, assistant(
		tool("mcp__mnemos__mnemos_get", map[string]any{"id": "ABC"}),
		tool("mcp__mnemos__mnemos_search", map[string]any{"query": "q"}),
		tool("mcp__mnemos__mnemos_save", map[string]any{"title": "t"}),
		tool("Read", map[string]any{"file_path": "/h/.claude/projects/-r/memory/note.md"}),
		tool("Read", map[string]any{"file_path": "/h/code/README.md"}),
		text("The canary rollout decision applies: every release goes through staging."),
	)))
	b.WriteString(line(t, map[string]any{"type": "attachment", "attachment": map[string]any{
		"type": "hook_success", "command": "/bin/mnemos prewarm", "content": "12345",
	}}))
	b.WriteString(line(t, map[string]any{"type": "attachment", "attachment": map[string]any{
		"type": "hook_success", "command": "/bin/mnemos hook pre-tool", "stdout": "1234567",
	}}))
	b.WriteString(line(t, map[string]any{"type": "attachment", "attachment": map[string]any{
		"type": "hook_success", "command": "/bin/other", "content": "ignored",
	}}))

	got, err := experiment.ReadTranscript(strings.NewReader(b.String()), experiment.Options{
		IsSynthetic: func(s string) bool { return strings.HasPrefix(s, "<synthetic>") },
		Titles: []string{
			"canary rollout decision",       // all three words together: a mention
			"canary pricing spreadsheet",    // one word only: not a mention
			"the decision",                  // too generic to detect: excluded
			"rollout decision canary fleet", // three of four is enough
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := experiment.Metrics{
		Prompts: 2, Pushback: 1, Opens: 1, Searches: 1, NoteReads: 1, Mentioned: 2, InjectedChars: 12,
	}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestReadTranscriptRequiresTitleWordsInOneMessage(t *testing.T) {
	var b strings.Builder
	b.WriteString(line(t, assistant(text("the canary question"))))
	b.WriteString(line(t, assistant(text("a rollout to consider"))))
	b.WriteString(line(t, assistant(text("our decision today"))))
	got, err := experiment.ReadTranscript(strings.NewReader(b.String()), experiment.Options{
		Titles: []string{"canary rollout decision"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Mentioned != 0 {
		t.Errorf("words scattered across messages are not a mention: %+v", got)
	}
}

func TestReadTranscriptSkipsOversizedLinesAndKeepsGoing(t *testing.T) {
	var b strings.Builder
	b.WriteString(line(t, userText("first prompt")))
	b.WriteString(line(t, userText(strings.Repeat("x", 5<<20))))
	b.WriteString(line(t, userText("second prompt")))
	got, err := experiment.ReadTranscript(strings.NewReader(b.String()), experiment.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Prompts != 2 {
		t.Errorf("want the two normal prompts, got %+v", got)
	}
}

func TestReadTranscriptHandlesAMissingFinalNewline(t *testing.T) {
	got, err := experiment.ReadTranscript(strings.NewReader(strings.TrimSpace(line(t, userText("only prompt")))), experiment.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Prompts != 1 {
		t.Errorf("last line lost: %+v", got)
	}
}

func TestPushbackDetector(t *testing.T) {
	detector := experiment.NewPushbackDetector()
	cases := []struct {
		prompt string
		want   bool
	}{
		{"tried it again still not working", true},
		{"restarted the server and its still slow", true},
		{"this got way worse than before", true},
		{"nah make it simpler", true},
		{"no, use the other table", true},
		{"you forgot the tests", true},
		{"i told you not to touch that", true},
		{"why did you remove the header", true},
		{"this is wrong", true},
		{"this is wrong, revert it", true},
		{"That’s wrong", true},
		{"add a dark mode toggle", false},
		{"no need to hurry", false},
		{"fix the login page", false},
		{"nobody uses this screen", false},
		{"please review the stillness animation", false},
		{"create a new export <pasted_content id=\"1\">it still fails for them</pasted_content>", false},
		{strings.Repeat("new brief with lots of detail ", 10) + "and it still needs a header", false},
	}
	for _, c := range cases {
		name := c.prompt
		if len(name) > 40 {
			name = name[:40]
		}
		t.Run(name, func(t *testing.T) {
			if got := detector.Matches(c.prompt); got != c.want {
				t.Errorf("Matches(%q) = %v, want %v", c.prompt, got, c.want)
			}
		})
	}
}

// conversations builds n measured conversations on arm with a prompt hook
// event per prompt, varied a little so intervals have a width.
func conversations(arm string, n int, m experiment.Metrics) []experiment.Conversation {
	out := make([]experiment.Conversation, n)
	for i := range out {
		out[i] = experiment.Conversation{Arm: arm, Found: true, Metrics: m}
		out[i].Metrics.Mentioned += i % 2
		out[i].Metrics.Pushback += i % 2
		out[i].Delivery = experiment.Delivery{PromptEvents: m.Prompts, Chars: 100 * m.Prompts}
	}
	return out
}

func build(push, index []experiment.Conversation) experiment.Report {
	return experiment.Build(append(append([]experiment.Conversation{}, push...), index...))
}

func TestBuildDecision(t *testing.T) {
	base := experiment.Metrics{Prompts: 12}
	with := func(mentioned, pushback int) experiment.Metrics {
		m := base
		m.Mentioned, m.Pushback = mentioned, pushback
		return m
	}
	cheaper := func(cs []experiment.Conversation) []experiment.Conversation {
		for i := range cs {
			cs[i].Delivery.Chars /= 4
		}
		return cs
	}
	cases := []struct {
		name         string
		push, index  []experiment.Conversation
		verdict, act string
	}{
		{"too few conversations", conversations(experiment.ArmPush, 5, base), conversations(experiment.ArmIndex, 50, base), "Too early", "Keep the experiment running"},
		{"too few prompts", conversations(experiment.ArmPush, 45, experiment.Metrics{Prompts: 2}), conversations(experiment.ArmIndex, 45, experiment.Metrics{Prompts: 2}), "Too early", "Keep the experiment running"},
		{"index clearly higher", conversations(experiment.ArmPush, 45, with(1, 0)), conversations(experiment.ArmIndex, 45, with(5, 0)), "Index replies used more memory", "Switch to index"},
		{"push clearly higher", conversations(experiment.ArmPush, 45, with(5, 0)), conversations(experiment.ArmIndex, 45, with(1, 0)), "Push replies used more memory", "Keep push"},
		{"guardrail blocks index", conversations(experiment.ArmPush, 45, with(1, 0)), conversations(experiment.ArmIndex, 45, with(5, 5)), "drew more pushback", "guardrail blocks"},
		{"no difference, index cheaper", conversations(experiment.ArmPush, 45, base), cheaper(conversations(experiment.ArmIndex, 45, base)), "Index injects", "Switch to index"},
		{"no difference, same cost", conversations(experiment.ArmPush, 45, base), conversations(experiment.ArmIndex, 45, base), "Neither is clearly cheaper", "fewest moving parts"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := build(c.push, c.index)
			if !strings.Contains(r.Verdict, c.verdict) || !strings.Contains(r.Decision, c.act) {
				t.Errorf("verdict %q / decision %q, want %q / %q", r.Verdict, r.Decision, c.verdict, c.act)
			}
		})
	}
}

func TestBuildCountsWhatItCannotMeasure(t *testing.T) {
	r := experiment.Build([]experiment.Conversation{
		{Arm: experiment.ArmPush, Found: true, Metrics: experiment.Metrics{Prompts: 4, Mentioned: 2}, Delivery: experiment.Delivery{PromptEvents: 4}},
		{Arm: experiment.ArmPush, Found: false},
		{Arm: experiment.ArmPush, Found: true, Metrics: experiment.Metrics{Prompts: 0}},
		{Arm: experiment.ArmPush, Found: true, Excluded: "scripted", Metrics: experiment.Metrics{Prompts: 9}},
		{Arm: experiment.ArmIndex, Found: true, Metrics: experiment.Metrics{Prompts: 2, Mentioned: 1, Pushback: 1}},
		{Arm: "something-else", Found: true, Metrics: experiment.Metrics{Prompts: 9}},
	})
	if r.Push.Assigned != 4 || r.Push.Excluded != 1 || r.Push.Missing != 1 || r.Push.Measured != 1 || r.Push.Totals.Prompts != 4 {
		t.Errorf("push summary: %+v", r.Push)
	}
	if r.Index.Assigned != 1 || r.Index.Measured != 1 {
		t.Errorf("index summary: %+v", r.Index)
	}
	if r.Push.HookCoverage != 1 || r.Index.HookCoverage != 0 {
		t.Errorf("hook coverage: push %v index %v", r.Push.HookCoverage, r.Index.HookCoverage)
	}
}

func TestRatesPoolPromptsAcrossConversations(t *testing.T) {
	// One long conversation with no mentions and one short one with two:
	// pooled, that is 2 mentions in 20 prompts, 10 per 100. A mean of
	// per-conversation rates would say 50 and let the short one dominate.
	r := experiment.Build([]experiment.Conversation{
		{Arm: experiment.ArmPush, Found: true, Metrics: experiment.Metrics{Prompts: 18}},
		{Arm: experiment.ArmPush, Found: true, Metrics: experiment.Metrics{Prompts: 2, Mentioned: 2}},
	})
	if got := r.Comparisons[0].Push; abs(got-10) > 1e-9 {
		t.Errorf("pooled rate = %v, want 10", got)
	}
}

func TestIntervalIsRepeatableAndCoversTheDifference(t *testing.T) {
	push := conversations(experiment.ArmPush, 45, experiment.Metrics{Prompts: 12, Mentioned: 1})
	index := conversations(experiment.ArmIndex, 45, experiment.Metrics{Prompts: 12, Mentioned: 3})
	a, b := build(push, index).Comparisons[0], build(push, index).Comparisons[0]
	if !a.HasInterval || a.Low != b.Low || a.High != b.High {
		t.Fatalf("the same data must give the same interval: %+v vs %+v", a, b)
	}
	if a.Low > a.Diff || a.High < a.Diff || a.Low <= 0 {
		t.Errorf("interval %v..%v should hold the difference %v and clear zero", a.Low, a.High, a.Diff)
	}
}

func TestDataProblemsBlockTheReading(t *testing.T) {
	ok := func() ([]experiment.Conversation, []experiment.Conversation) {
		return conversations(experiment.ArmPush, 45, experiment.Metrics{Prompts: 12, Mentioned: 1}),
			conversations(experiment.ArmIndex, 45, experiment.Metrics{Prompts: 12, Mentioned: 5})
	}

	push, index := ok()
	if r := build(push, index); !r.Ready || len(r.Problems) != 0 {
		t.Fatalf("clean data should be readable: %+v", r.Problems)
	}

	push, index = ok()
	index[0].Metrics.Prompts, index[1].Metrics.Prompts = 400, 400
	if r := build(push, index); r.Ready || !hasProblem(r, "two longest conversations") {
		t.Errorf("two dominant conversations must block the reading: %v", r.Problems)
	}

	push, index = ok()
	for i := range push {
		push[i].Delivery.PromptEvents = 0
	}
	if r := build(push, index); r.Ready || !hasProblem(r, "prompt hook event") {
		t.Errorf("missing hook events must block the reading: %v", r.Problems)
	}

	push, index = ok()
	push = append(push, conversations(experiment.ArmPush, 60, experiment.Metrics{Prompts: 12})...)
	if r := build(push, index); r.Ready || !hasProblem(r, "lopsided") {
		t.Errorf("a broken split must block the reading: %v", r.Problems)
	}
}

func hasProblem(r experiment.Report, s string) bool {
	for _, p := range r.Problems {
		if strings.Contains(p, s) {
			return true
		}
	}
	return false
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
