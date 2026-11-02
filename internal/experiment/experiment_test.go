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

func conversations(arm string, n int, m experiment.Metrics) []experiment.Conversation {
	out := make([]experiment.Conversation, n)
	for i := range out {
		out[i] = experiment.Conversation{Arm: arm, Found: true, Metrics: m}
		// A little variation so intervals have a width.
		out[i].Metrics.Mentioned += i % 2
		out[i].Metrics.Pushback += i % 2
	}
	return out
}

func TestBuildVerdict(t *testing.T) {
	base := experiment.Metrics{Prompts: 10}
	withMentions := func(n int) experiment.Metrics {
		m := base
		m.Mentioned = n
		return m
	}
	withPushback := func(n int) experiment.Metrics {
		m := base
		m.Pushback = n
		return m
	}
	cases := []struct {
		name string
		push []experiment.Conversation
		idx  []experiment.Conversation
		want string
	}{
		{
			"too few conversations",
			conversations(experiment.ArmPush, 5, base),
			conversations(experiment.ArmIndex, 40, base),
			"Too early",
		},
		{
			"index clearly higher",
			conversations(experiment.ArmPush, 30, withMentions(1)),
			conversations(experiment.ArmIndex, 30, withMentions(5)),
			"Index sessions used more memory",
		},
		{
			"push clearly higher",
			conversations(experiment.ArmPush, 30, withMentions(5)),
			conversations(experiment.ArmIndex, 30, withMentions(1)),
			"Push sessions used more memory",
		},
		{
			"no difference",
			conversations(experiment.ArmPush, 30, base),
			conversations(experiment.ArmIndex, 30, base),
			"No measurable difference",
		},
		{
			"guardrail warns against index",
			conversations(experiment.ArmPush, 30, base),
			conversations(experiment.ArmIndex, 30, withPushback(5)),
			"Warning: index sessions drew more pushback",
		},
		{
			"guardrail warns against push",
			conversations(experiment.ArmPush, 30, withPushback(5)),
			conversations(experiment.ArmIndex, 30, base),
			"Warning: push sessions drew more pushback",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := experiment.Build(append(append([]experiment.Conversation{}, c.push...), c.idx...))
			if !strings.Contains(r.Verdict, c.want) {
				t.Errorf("verdict %q does not contain %q", r.Verdict, c.want)
			}
		})
	}
}

func TestBuildExcludesWhatItCannotMeasure(t *testing.T) {
	r := experiment.Build([]experiment.Conversation{
		{Arm: experiment.ArmPush, Found: true, Metrics: experiment.Metrics{Prompts: 4, Mentioned: 2}},
		{Arm: experiment.ArmPush, Found: false},
		{Arm: experiment.ArmPush, Found: true, Metrics: experiment.Metrics{Prompts: 0}},
		{Arm: experiment.ArmIndex, Found: true, Metrics: experiment.Metrics{Prompts: 2, Mentioned: 1, Pushback: 1}},
		{Arm: "something-else", Found: true, Metrics: experiment.Metrics{Prompts: 9}},
	})
	if r.Push.Assigned != 3 || r.Push.Measured != 1 || r.Push.Totals.Prompts != 4 {
		t.Errorf("push summary: %+v", r.Push)
	}
	if r.Index.Assigned != 1 || r.Index.Measured != 1 {
		t.Errorf("index summary: %+v", r.Index)
	}
	pushback := r.Comparisons[1]
	if pushback.PushN != 1 || pushback.IndexN != 0 {
		t.Errorf("conversations under three prompts must stay out of the rate: %+v", pushback)
	}
}

func TestBuildIntervalMatchesWelchByHand(t *testing.T) {
	// Push mentions are 0 and 2, index mentions are 4 and 6. Each has mean
	// (1, 5) and sample variance 2, so the margin is 1.96 * sqrt(2/2 + 2/2).
	r := experiment.Build([]experiment.Conversation{
		{Arm: experiment.ArmPush, Found: true, Metrics: experiment.Metrics{Prompts: 1, Mentioned: 0}},
		{Arm: experiment.ArmPush, Found: true, Metrics: experiment.Metrics{Prompts: 1, Mentioned: 2}},
		{Arm: experiment.ArmIndex, Found: true, Metrics: experiment.Metrics{Prompts: 1, Mentioned: 4}},
		{Arm: experiment.ArmIndex, Found: true, Metrics: experiment.Metrics{Prompts: 1, Mentioned: 6}},
	})
	c := r.Comparisons[0]
	if c.Diff != 4 {
		t.Errorf("diff = %v", c.Diff)
	}
	const margin = 2.771859
	if abs(c.Low-(4-margin)) > 1e-4 || abs(c.High-(4+margin)) > 1e-4 {
		t.Errorf("interval %v..%v", c.Low, c.High)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
