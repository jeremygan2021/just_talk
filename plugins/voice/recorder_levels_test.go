package voice

import (
	"encoding/binary"
	"testing"
)

// s16le builds a little-endian s16 PCM byte slice from the given samples.
func s16le(samples []int16) []byte {
	buf := make([]byte, 0, len(samples)*2)
	for _, s := range samples {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], uint16(s))
		buf = append(buf, b[:]...)
	}
	return buf
}

func TestRecorderLevelsEmpty(t *testing.T) {
	r := NewRecorder(nil, 1)
	samples := r.Levels()
	if len(samples) != levelRingSize {
		t.Fatalf("empty recorder should return %d zero samples, got %d", levelRingSize, len(samples))
	}
	for i, v := range samples {
		if v != 0 {
			t.Fatalf("empty Levels[%d] = %f, want 0", i, v)
		}
	}
}

func TestRecorderLevelCount(t *testing.T) {
	r := NewRecorder(nil, 1)
	if got := r.LevelCount(); got != 0 {
		t.Fatalf("empty LevelCount = %d, want 0", got)
	}
	r.recordLevel(s16le([]int16{100}))
	r.recordLevel(s16le([]int16{200}))
	if got := r.LevelCount(); got != 2 {
		t.Fatalf("LevelCount = %d, want 2", got)
	}
	for i := 0; i < levelRingSize+5; i++ {
		r.recordLevel(s16le([]int16{int16(i + 1)}))
	}
	want := 2 + levelRingSize + 5
	if got := r.LevelCount(); got != want {
		t.Fatalf("LevelCount after overflow = %d, want %d", got, want)
	}
}

func TestRecorderLevelsPartialRing(t *testing.T) {
	r := NewRecorder(nil, 1)
	// Push 3 chunks: each chunk should record one normalized peak.
	r.recordLevel(s16le([]int16{100, 200, 300, 400}))
	r.recordLevel(s16le([]int16{1000, 2000, -3000, 4000}))
	r.recordLevel(s16le([]int16{-32000, 32000}))
	samples := r.Levels()
	if len(samples) != 3 {
		t.Fatalf("Levels len = %d, want 3 (under ring capacity)", len(samples))
	}
	// Expected normalized peaks (peak / 32768):
	//   chunk 1: max(|100|,|200|,|300|,|400|) = 400
	//   chunk 2: max(|1000|,|2000|,|3000|,|4000|) = 4000
	//   chunk 3: max(|-32000|,|32000|) = 32000
	want := []float32{400.0 / 32768.0, 4000.0 / 32768.0, 32000.0 / 32768.0}
	for i, w := range want {
		if samples[i] != w {
			t.Fatalf("Levels[%d] = %f, want %f", i, samples[i], w)
		}
	}
}

func TestRecorderLevelsFullRingWraps(t *testing.T) {
	r := NewRecorder(nil, 1)
	// Push more than the ring capacity.
	total := levelRingSize*2 + 5
	expected := make([]float32, 0, total)
	for i := 0; i < total; i++ {
		// Make each sample's peak distinct so we can verify ordering.
		v := int16(i + 1)
		if v < 0 {
			v = 0
		}
		r.recordLevel(s16le([]int16{v}))
		expected = append(expected, float32(v)/32768.0)
	}
	samples := r.Levels()
	if len(samples) != levelRingSize {
		t.Fatalf("Levels len = %d, want %d (full ring)", len(samples), levelRingSize)
	}
	// The last `levelRingSize` entries of `expected` should be the
	// ring contents, in chronological order (oldest first).
	want := expected[len(expected)-levelRingSize:]
	for i, w := range want {
		if samples[i] != w {
			t.Fatalf("wrapped Levels[%d] = %f, want %f", i, samples[i], w)
		}
	}
}

func TestRecorderLevelsSilentChunk(t *testing.T) {
	r := NewRecorder(nil, 1)
	r.recordLevel(s16le([]int16{0, 0, 0}))
	r.recordLevel(s16le([]int16{0, 0}))
	samples := r.Levels()
	if len(samples) != 2 {
		t.Fatalf("Levels len = %d, want 2", len(samples))
	}
	for i, v := range samples {
		if v != 0 {
			t.Fatalf("silent Levels[%d] = %f, want 0", i, v)
		}
	}
}

func TestRecorderLevelsIndependentCopies(t *testing.T) {
	r := NewRecorder(nil, 1)
	r.recordLevel(s16le([]int16{1000}))
	first := r.Levels()
	first[0] = 42
	second := r.Levels()
	if second[0] == 42 {
		t.Fatal("Levels must return a copy, not the internal ring")
	}
}