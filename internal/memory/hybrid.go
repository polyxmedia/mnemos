package memory

import "math"

// HybridParams tunes the BM25 + cosine-similarity fusion in Service.Search.
// Alpha is the weight of BM25 in the final Reciprocal Rank Fusion score;
// (1-Alpha) is the weight of cosine. Alpha=1.0 is pure BM25; Alpha=0.0
// is pure semantic. The 2026 LongMemEval research says the sweet spot is
// near 0.5 — BM25 catches exact identifiers, cosine catches paraphrases.
type HybridParams struct {
	Alpha float64 // 0..1, default 0.5
	K     int     // RRF constant, default 60
}

// DefaultHybridParams returns literature-backed defaults.
func DefaultHybridParams() HybridParams {
	return HybridParams{Alpha: 0.5, K: 60}
}

// cosine computes the cosine similarity between two float32 vectors. Zero
// length or mismatched lengths return 0 so callers can treat "no vector"
// as "no signal" without branches in the hot path.
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// rrfScore implements Reciprocal Rank Fusion for a single item: the
// combined score is alpha * 1/(k+rankBM25) + (1-alpha) * 1/(k+rankCos).
// Ranks are 1-based; infinity ranks (item missing from a list) contribute 0.
func rrfScore(rankBM25, rankCos int, p HybridParams) float64 {
	var s float64
	if rankBM25 > 0 {
		s += p.Alpha / float64(p.K+rankBM25)
	}
	if rankCos > 0 {
		s += (1 - p.Alpha) / float64(p.K+rankCos)
	}
	return s
}
