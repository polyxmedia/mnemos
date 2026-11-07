package experiment_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/experiment"
)

func labelFixture() ([]experiment.Event, map[string]experiment.MemoryText) {
	texts := map[string]experiment.MemoryText{}
	var events []experiment.Event
	for i := 0; i < 10; i++ {
		e := experiment.Event{ID: fmt.Sprintf("e%d", i), Channel: "prompt_hook", Query: fmt.Sprintf("prompt %d", i)}
		for j, score := range []float64{6, 2, 1} {
			id := fmt.Sprintf("m%d-%d", i, j)
			texts[id] = experiment.MemoryText{Title: "title " + id, Content: "content " + id}
			e.Candidates = append(e.Candidates, experiment.Scored{ID: id, Score: score})
		}
		if i%2 == 0 {
			e.Injected = []string{e.Candidates[0].ID, e.Candidates[1].ID}
		}
		events = append(events, e)
	}
	// Nothing to judge: never sampled.
	events = append(events, experiment.Event{ID: "empty", Channel: "prompt_hook"})
	return events, texts
}

func TestSampleIsRepeatableAndBlind(t *testing.T) {
	events, texts := labelFixture()
	a := experiment.Sample(events, texts, 4, 7)
	b := experiment.Sample(events, texts, 4, 7)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the same seed must give the same sample")
	}
	seen := map[string]bool{}
	for _, p := range a {
		seen[p.Event] = true
		if p.Event == "empty" {
			t.Error("an event with nothing to judge was sampled")
		}
		if p.Relevant != nil {
			t.Error("pairs must start unlabelled")
		}
	}
	if len(seen) != 4 || len(a) != 12 {
		t.Errorf("want 4 events and 12 pairs, got %d events and %d pairs", len(seen), len(a))
	}
	line, _ := json.Marshal(a[0])
	for _, leak := range []string{"score", "arm", "injected", "shown"} {
		if strings.Contains(strings.ToLower(string(line)), `"`+leak) {
			t.Errorf("a pair must not say %q: %s", leak, line)
		}
	}
	if c := experiment.Sample(events, texts, 4, 8); reflect.DeepEqual(a, c) {
		t.Error("another seed should give another sample")
	}
}

func TestScoreReadsLabelsAgainstTheLog(t *testing.T) {
	events, texts := labelFixture()
	byID := map[string]experiment.Event{}
	for _, e := range events {
		byID[e.ID] = e
	}
	pairs := experiment.Sample(events, texts, 0, 1)
	yes, no := true, false
	for i := range pairs {
		// Only the top candidate (score 6) of each prompt is relevant.
		if strings.HasSuffix(pairs[i].Memory, "-0") {
			pairs[i].Relevant = &yes
		} else {
			pairs[i].Relevant = &no
		}
	}
	pairs = append(pairs, experiment.Pair{Event: "gone", Memory: "x", Relevant: &yes})

	s := experiment.Score(pairs, byID)
	if s.Labelled != 30 || s.Events != 10 {
		t.Fatalf("labelled %d over %d events", s.Labelled, s.Events)
	}
	// Five prompts showed two memories each, one of them relevant.
	if got := s.Shown["prompt_hook"]; got.Hits != 5 || got.Total != 10 {
		t.Errorf("shown precision = %+v", got)
	}
	if s.Helpful.Hits != 10 || s.Helpful.Total != 10 {
		t.Errorf("helpful = %+v", s.Helpful)
	}
	want := map[float64][2]int{0: {10, 30}, 1.5: {10, 20}, 3: {10, 10}, 8: {0, 0}}
	for _, f := range s.Floors {
		if w, ok := want[f.Floor]; ok && (f.Precision.Hits != w[0] || f.Precision.Total != w[1]) {
			t.Errorf("floor %.1f: %+v, want %v", f.Floor, f.Precision, w)
		}
	}
	if p := s.Shown["prompt_hook"]; p.Low >= 0.5 || p.High <= 0.5 {
		t.Errorf("Wilson interval should hold 0.5: %+v", p)
	}
}
