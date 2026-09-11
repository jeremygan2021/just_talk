package overlay

import (
	"math"
	"testing"
	"time"
)

func stepAnimator(a *waveformAnimator, start time.Time, frames int, levels []float32) (time.Time, []float32) {
	var bars []float32
	now := start
	for i := 0; i < frames; i++ {
		now = now.Add(33 * time.Millisecond)
		bars = a.Update(now, levels)
	}
	return now, bars
}

func maxOf(bars []float32) float32 {
	var m float32
	for _, v := range bars {
		if v > m {
			m = v
		}
	}
	return m
}

func TestAnimatorStartsFlat(t *testing.T) {
	a := newWaveformAnimator(barCount)
	if got := a.Update(time.Unix(0, 0), nil); len(got) != barCount {
		t.Fatalf("bars len = %d, want %d", len(got), barCount)
	}
	if !a.Settled() {
		t.Fatal("a fresh animator must be settled (flat) before any audio")
	}
	for i, v := range a.bars {
		if v != 0 {
			t.Fatalf("bars[%d] = %f, want 0 with no audio", i, v)
		}
	}
}

func TestAnimatorReactsQuicklyToSpeech(t *testing.T) {
	a := newWaveformAnimator(barCount)
	_, bars := stepAnimator(a, time.Unix(0, 0), 1, []float32{0.25})
	if maxOf(bars) < 0.15 {
		t.Fatalf("bars should move within one frame of speech, max = %f", maxOf(bars))
	}
	if a.Settled() {
		t.Fatal("animator must not report settled while speech is coming in")
	}
}

// TestAnimatorStaysLively is the core of the "call indicator" behaviour:
// with a constant input level the bars must keep moving (different bars at
// different speeds), not freeze into one static shape.
func TestAnimatorStaysLively(t *testing.T) {
	a := newWaveformAnimator(barCount)
	now := time.Unix(0, 0)
	var prev []float32
	movement := 0
	for i := 0; i < 40; i++ {
		now = now.Add(33 * time.Millisecond)
		cur := append([]float32(nil), a.Update(now, []float32{0.22})...)
		if prev != nil {
			for j := range cur {
				if math.Abs(float64(cur[j]-prev[j])) > 0.03 {
					movement++
				}
			}
		}
		prev = cur
	}
	if movement == 0 {
		t.Fatal("bars must keep moving while a constant level is present")
	}
}

func TestAnimatorCentreBarsLeadEdges(t *testing.T) {
	a := newWaveformAnimator(barCount)
	centre := barCount / 2
	best := 0.0
	for i := 0; i < 40; i++ {
		_, bars := stepAnimator(a, time.Unix(0, 0).Add(time.Duration(i)*33*time.Millisecond), 1, []float32{0.3})
		if diff := float64(bars[centre] - bars[0]); diff > best {
			best = diff
		}
	}
	if best < 0.1 {
		t.Fatalf("centre bars should lead the edges, best gap = %f", best)
	}
}

func TestAnimatorFlattensWhenSilent(t *testing.T) {
	a := newWaveformAnimator(barCount)
	now, _ := stepAnimator(a, time.Unix(0, 0), 12, []float32{0.3})
	if maxOf(a.bars) < 0.2 {
		t.Fatalf("expected loud bars before going silent, max = %f", maxOf(a.bars))
	}

	// Silence: the envelope decays and every bar eases to a flat line.
	now, bars := stepAnimator(a, now, 40, []float32{0})
	if maxOf(bars) != 0 {
		t.Fatalf("bars must be exactly flat after silence, max = %f", maxOf(bars))
	}
	if !a.Settled() {
		t.Fatal("animator should report settled once the bars are flat")
	}
}

// TestAnimatorReleaseIsGradual guards the "not a strip chart" feel: after
// the audio stops the bars must ease down instead of snapping to zero in
// one frame.
func TestAnimatorReleaseIsGradual(t *testing.T) {
	a := newWaveformAnimator(barCount)
	now, _ := stepAnimator(a, time.Unix(0, 0), 12, []float32{0.3})
	before := maxOf(a.bars)

	now = now.Add(33 * time.Millisecond)
	after := maxOf(a.Update(now, []float32{0}))

	if after >= before {
		t.Fatalf("bars should start decaying, before=%f after=%f", before, after)
	}
	if after <= 0.05 {
		t.Fatalf("bars must not collapse in a single frame, after=%f", after)
	}
}

func TestAnimatorDeterministic(t *testing.T) {
	levels := []float32{0.1, 0.25}
	a := newWaveformAnimator(barCount)
	b := newWaveformAnimator(barCount)
	start := time.Unix(100, 0)
	for i := 0; i < 25; i++ {
		now := start.Add(time.Duration(i) * 33 * time.Millisecond)
		got, want := a.Update(now, levels), b.Update(now, levels)
		for j := range got {
			if got[j] != want[j] {
				t.Fatalf("frame %d bar %d: %f != %f", i, j, got[j], want[j])
			}
		}
	}
}

func TestAnimatorClampsLargeTimeStep(t *testing.T) {
	a := newWaveformAnimator(barCount)
	start := time.Unix(0, 0)
	stepAnimator(a, start, 5, []float32{0.3})
	// A long stall (debugger, suspended laptop) must not make the clock
	// jump or push the bars out of range.
	bars := a.Update(start.Add(10*time.Minute), []float32{0.3})
	for i, v := range bars {
		if v < 0 || v > 1 || math.IsNaN(float64(v)) {
			t.Fatalf("bars[%d] = %f out of range after a long stall", i, v)
		}
	}
}

func TestAnimatorResetFlattens(t *testing.T) {
	a := newWaveformAnimator(barCount)
	stepAnimator(a, time.Unix(0, 0), 10, []float32{0.3})
	a.Reset()
	if !a.Settled() {
		t.Fatal("Reset must return the animator to a flat line")
	}
	if a.clock != 0 {
		t.Fatalf("Reset must restart the oscillator clock, got %f", a.clock)
	}
}

func TestLevelTargetMapping(t *testing.T) {
	cases := []struct {
		in   []float32
		want float64
		tol  float64
	}{
		{nil, 0, 0.001},
		{[]float32{0}, 0, 0.001},
		{[]float32{silenceFloor}, 0, 0.001},
		{[]float32{silenceFloor - 0.01}, 0, 0.001},
		{[]float32{loudRef}, 1, 0.001},
		{[]float32{1}, 1, 0.001},
	}
	for _, tc := range cases {
		if got := levelTarget(tc.in); math.Abs(got-tc.want) > tc.tol {
			t.Errorf("levelTarget(%v) = %f, want %f", tc.in, got, tc.want)
		}
	}

	// Mid-level speech lifts perceptually (sqrt curve) so quiet speech
	// still moves the bars.
	mid := levelTarget([]float32{0.1})
	if mid < 0.4 || mid > 0.7 {
		t.Fatalf("levelTarget(0.1) = %f, want a lifted mid-range value", mid)
	}

	// A short peak that already vanished from the newest sample still
	// counts, so the bars do not flicker between chunks.
	if got := levelTarget([]float32{0.3, 0.0}); got != 1 {
		t.Fatalf("the previous sample should keep a short peak alive, got %f", got)
	}
}
