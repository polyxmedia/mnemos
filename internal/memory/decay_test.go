package memory

import (
	"math"
	"testing"
	"time"
)

func TestImportanceMovesScore(t *testing.T) {
	r := NewRanker(DefaultRankParams())
	now := time.Now().UTC()
	low := Observation{Importance: 1, CreatedAt: now}
	high := Observation{Importance: 10, CreatedAt: now}
	if r.Score(high, 1.0, now) <= r.Score(low, 1.0, now) {
		t.Errorf("high importance should outrank low")
	}
}

func TestAccessCountBoostsScore(t *testing.T) {
	r := NewRanker(DefaultRankParams())
	now := time.Now().UTC()
	cold := Observation{Importance: 5, CreatedAt: now, AccessCount: 0}
	hot := Observation{Importance: 5, CreatedAt: now, AccessCount: 100}
	if r.Score(hot, 1.0, now) <= r.Score(cold, 1.0, now) {
		t.Errorf("frequently-accessed should outrank cold")
	}
}

func TestImportanceClampedOutOfRange(t *testing.T) {
	r := NewRanker(DefaultRankParams())
	now := time.Now().UTC()
	// Importance 0 or 11 should clamp into [1, 10].
	neg := Observation{Importance: 0, CreatedAt: now}
	over := Observation{Importance: 11, CreatedAt: now}
	if r.Score(neg, 1.0, now) == 0 || math.IsNaN(r.Score(over, 1.0, now)) {
		t.Errorf("out-of-range importance should not produce zero/NaN")
	}
}

func TestDecayAtFutureCreatedAtTreatedAsZero(t *testing.T) {
	r := NewRanker(DefaultRankParams())
	now := time.Now().UTC()
	future := Observation{Importance: 5, CreatedAt: now.Add(time.Hour)}
	present := Observation{Importance: 5, CreatedAt: now}
	// Should be equal — future age clamps to 0.
	if math.Abs(r.Score(future, 1.0, now)-r.Score(present, 1.0, now)) > 1e-6 {
		t.Errorf("future createdAt should clamp age to 0")
	}
}

func TestHashContentStableAcrossWhitespace(t *testing.T) {
	h1 := hashContent(TypeDecision, "Use X", "we use X")
	h2 := hashContent(TypeDecision, "  Use X  ", "we use X\n")
	if h1 != h2 {
		t.Errorf("whitespace should not change hash")
	}
}

func TestHashContentSensitiveToType(t *testing.T) {
	h1 := hashContent(TypeDecision, "Use X", "we use X")
	h2 := hashContent(TypeBugfix, "Use X", "we use X")
	if h1 == h2 {
		t.Errorf("different types should hash differently")
	}
}
