package experiment

import (
	"math"
	"math/rand/v2"
	"sort"
)

// Event is one logged delivery decision, as the labelling tools need it.
type Event struct {
	ID         string
	Channel    string
	Project    string
	Query      string
	Candidates []Scored
	Injected   []string
}

// Scored is a candidate memory and its retrieval score.
type Scored struct {
	ID    string
	Score float64
}

// MemoryText is what a labeller sees of a memory.
type MemoryText struct {
	Title   string
	Content string
	Project string
}

// Pair is one prompt and one memory for a labeller to judge. It carries no
// arm, no score and no sign of whether the memory was shown, and the
// memories of an event come in random order, so the label cannot lean on
// what the system already decided.
type Pair struct {
	Event         string `json:"event"`
	Channel       string `json:"channel"`
	Project       string `json:"project"`
	Query         string `json:"query"`
	Memory        string `json:"memory"`
	MemoryProject string `json:"memory_project"`
	Title         string `json:"title"`
	Content       string `json:"content"`
	// Relevant is filled in by the labeller: true when knowing this memory
	// would change or support the response to the query.
	Relevant *bool `json:"relevant"`
}

// maxPairContent bounds a memory's text in a pair.
const maxPairContent = 1200

// Sample picks up to n events with something to judge and lays out one pair
// per memory each considered or showed. The seed makes the sample
// repeatable. Memories missing from texts are skipped.
func Sample(events []Event, texts map[string]MemoryText, n int, seed uint64) []Pair {
	var pool []Event
	for _, e := range events {
		if len(memoryIDs(e)) > 0 {
			pool = append(pool, e)
		}
	}
	rng := rand.New(rand.NewPCG(seed, 0))
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	if n > 0 && len(pool) > n {
		pool = pool[:n]
	}
	var out []Pair
	for _, e := range pool {
		ids := memoryIDs(e)
		rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
		for _, id := range ids {
			t, ok := texts[id]
			if !ok {
				continue
			}
			content := t.Content
			if len(content) > maxPairContent {
				content = content[:maxPairContent] + "..."
			}
			out = append(out, Pair{
				Event: e.ID, Channel: e.Channel, Project: e.Project, Query: e.Query,
				Memory: id, MemoryProject: t.Project, Title: t.Title, Content: content,
			})
		}
	}
	return out
}

// memoryIDs lists the memories an event considered or showed, once each.
func memoryIDs(e Event) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, c := range e.Candidates {
		add(c.ID)
	}
	for _, id := range e.Injected {
		add(id)
	}
	return ids
}

// Proportion is a count of hits out of a total with a 95% Wilson interval.
type Proportion struct {
	Hits  int
	Total int
	Rate  float64
	Low   float64
	High  float64
}

func proportion(hits, total int) Proportion {
	p := Proportion{Hits: hits, Total: total}
	if total == 0 {
		return p
	}
	const z = 1.96
	n := float64(total)
	p.Rate = float64(hits) / n
	centre := (p.Rate + z*z/(2*n)) / (1 + z*z/n)
	half := z * math.Sqrt(p.Rate*(1-p.Rate)/n+z*z/(4*n*n)) / (1 + z*z/n)
	p.Low, p.High = math.Max(0, centre-half), math.Min(1, centre+half)
	return p
}

// FloorRow is what the prompt hook would show at one score floor: how many
// memories clear it and how many of those were labelled relevant.
type FloorRow struct {
	Floor     float64
	Precision Proportion
	// PerEvent is memories shown per labelled event at this floor.
	PerEvent float64
}

// Scores summarises labelled pairs.
type Scores struct {
	Labelled int
	Events   int
	// Shown is the precision of what each channel actually injected.
	Shown map[string]Proportion
	// Helpful is the share of labelled prompt events where at least one
	// candidate was relevant: how often memory had something to offer.
	Helpful Proportion
	// Floors replays the prompt hook's top three at other score floors,
	// over both arms, since the index arm logs what push would have shown.
	Floors []FloorRow
}

// ScoreFloors are the prompt hook floors Score replays. 1.5 is the floor
// in use when the event log started.
var ScoreFloors = []float64{0, 1.5, 2, 3, 4, 6, 8}

// Score reads labelled pairs against the events they came from. Pairs with
// no label, or whose event is unknown, are skipped.
func Score(pairs []Pair, events map[string]Event) Scores {
	s := Scores{Shown: map[string]Proportion{}}
	type tally struct{ hits, total int }
	shown := map[string]*tally{}
	floorHits := make([]int, len(ScoreFloors))
	floorTotal := make([]int, len(ScoreFloors))
	eventSeen := map[string]bool{}
	promptEvents := map[string]bool{}
	helpful := map[string]bool{}

	for _, p := range pairs {
		if p.Relevant == nil {
			continue
		}
		e, ok := events[p.Event]
		if !ok {
			continue
		}
		s.Labelled++
		eventSeen[e.ID] = true
		rel := *p.Relevant
		if contains(e.Injected, p.Memory) {
			t := shown[e.Channel]
			if t == nil {
				t = &tally{}
				shown[e.Channel] = t
			}
			t.total++
			if rel {
				t.hits++
			}
		}
		if e.Channel != "prompt_hook" {
			continue
		}
		promptEvents[e.ID] = true
		if rel {
			helpful[e.ID] = true
		}
		score, ok := candidateScore(e, p.Memory)
		if !ok {
			continue
		}
		for i, floor := range ScoreFloors {
			if score >= floor {
				floorTotal[i]++
				if rel {
					floorHits[i]++
				}
			}
		}
	}
	s.Events = len(eventSeen)
	for ch, t := range shown {
		s.Shown[ch] = proportion(t.hits, t.total)
	}
	s.Helpful = proportion(len(helpful), len(promptEvents))
	for i, floor := range ScoreFloors {
		row := FloorRow{Floor: floor, Precision: proportion(floorHits[i], floorTotal[i])}
		if len(promptEvents) > 0 {
			row.PerEvent = float64(floorTotal[i]) / float64(len(promptEvents))
		}
		s.Floors = append(s.Floors, row)
	}
	sort.Slice(s.Floors, func(i, j int) bool { return s.Floors[i].Floor < s.Floors[j].Floor })
	return s
}

func candidateScore(e Event, id string) (float64, bool) {
	for _, c := range e.Candidates {
		if c.ID == id {
			return c.Score, true
		}
	}
	return 0, false
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
