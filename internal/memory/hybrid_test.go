package memory

import (
	"math"
	"testing"
)

func TestCosineOrthogonalIsZero(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	if c := cosine(a, b); c != 0 {
		t.Errorf("orthogonal cosine should be 0, got %v", c)
	}
}

func TestCosineIdenticalIsOne(t *testing.T) {
	a := []float32{0.3, 0.4, 0.5}
	if c := cosine(a, a); math.Abs(c-1) > 1e-6 {
		t.Errorf("identical cosine should be 1, got %v", c)
	}
}

func TestCosineLengthMismatchReturnsZero(t *testing.T) {
	if c := cosine([]float32{1}, []float32{1, 0}); c != 0 {
		t.Errorf("mismatched lengths should return 0, got %v", c)
	}
	if c := cosine(nil, []float32{1}); c != 0 {
		t.Errorf("nil operand should return 0")
	}
}

func TestRRFScoreFavoursBothLists(t *testing.T) {
	p := DefaultHybridParams()
	// Item ranked #1 in both lists outranks item ranked #1 in only one.
	both := rrfScore(1, 1, p)
	onlyBM25 := rrfScore(1, 0, p)
	onlyCos := rrfScore(0, 1, p)
	if both <= onlyBM25 || both <= onlyCos {
		t.Errorf("both-lists rank should beat single-list: both=%v bm25=%v cos=%v",
			both, onlyBM25, onlyCos)
	}
}

func TestRRFAlphaTiltsBalance(t *testing.T) {
	// Alpha = 1.0 should ignore cosine.
	p := HybridParams{Alpha: 1.0, K: 60}
	bm25Only := rrfScore(1, 999, p)
	pureBM25 := rrfScore(1, 0, p)
	if bm25Only != pureBM25 {
		t.Errorf("alpha=1 should ignore cos rank: got %v vs %v", bm25Only, pureBM25)
	}
}
