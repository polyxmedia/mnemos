package storage

import (
	"math"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := []float32{0, 1, -1, 0.5, math.Pi, math.MaxFloat32, -math.MaxFloat32}
	out := decodeVector(encodeVector(in).([]byte))
	if len(out) != len(in) {
		t.Fatalf("len mismatch: in=%d out=%d", len(in), len(out))
	}
	for i := range in {
		if in[i] != out[i] {
			t.Errorf("idx %d: in=%v out=%v", i, in[i], out[i])
		}
	}
}

func TestEncodeNilReturnsNil(t *testing.T) {
	if v := encodeVector(nil); v != nil {
		t.Errorf("encode(nil) should be nil, got %v", v)
	}
	if v := encodeVector([]float32{}); v != nil {
		t.Errorf("encode(empty) should be nil, got %v", v)
	}
}

func TestDecodeMalformedReturnsNil(t *testing.T) {
	if v := decodeVector([]byte{1, 2, 3}); v != nil {
		t.Errorf("decode(non-4-multiple) should be nil, got %v", v)
	}
	if v := decodeVector(nil); v != nil {
		t.Errorf("decode(nil) should be nil, got %v", v)
	}
}
