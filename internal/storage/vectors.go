package storage

import (
	"encoding/binary"
	"math"
)

// encodeVector serialises a float32 slice as a little-endian BLOB. Returns
// nil for empty/nil vectors so the column stays NULL in SQLite.
func encodeVector(v []float32) any {
	if len(v) == 0 {
		return nil
	}
	buf := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(x))
	}
	return buf
}

// decodeVector inverts encodeVector. Returns nil for empty or malformed
// input (anything not a multiple of 4 bytes).
func decodeVector(buf []byte) []float32 {
	if len(buf) == 0 || len(buf)%4 != 0 {
		return nil
	}
	out := make([]float32, len(buf)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
	}
	return out
}
