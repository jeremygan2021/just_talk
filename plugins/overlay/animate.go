package overlay

import (
	"math"
	"time"
)

// waveformAnimator turns the recorder's coarse peak-amplitude samples
// into the lively, call-indicator style bar animation the overlay
// renders.
//
// The recorder produces one peak per PCM chunk (tens of milliseconds
// apart), so scrolling those samples from left to right both looks
// steppy and reads as "oldest on the left" instead of "loud right now".
// The animator instead keeps a single smoothed energy envelope driven by
// the newest samples and lets a bank of oscillators dance inside it:
//
//   - audio present -> bars swell immediately and ripple outwards
//   - audio absent  -> the envelope decays and every bar eases flat
//
// That is the "on a call" feel: motion that reacts to the voice rather
// than a strip chart scrolling sideways.
type waveformAnimator struct {
	bars   []float32
	phases []float64
	freqs  []float64
	energy float64
	clock  float64
	lastAt time.Time
}

const (
	// silenceFloor and loudRef map raw peak amplitudes onto the 0..1
	// envelope. Typical speech peaks sit around 0.05-0.3, so the gate
	// keeps room tone flat while normal speech reaches the top.
	silenceFloor = 0.02
	loudRef      = 0.30

	// attackTau/releaseTau shape the energy envelope: quick to react,
	// unhurried to fall, which is what makes the bars feel springy.
	attackTau  = 0.028
	releaseTau = 0.190

	// Per-bar smoothing, a touch slower than the envelope so the bars
	// trail the energy instead of snapping.
	barAttackTau  = 0.024
	barReleaseTau = 0.085

	// animatorFrame is the nominal frame time used for the first update.
	animatorFrame = 33 * time.Millisecond
	// animatorMaxStep bounds one frame's dt so a stalled overlay cannot
	// make the animation jump.
	animatorMaxStep = 90 * time.Millisecond
)

func newWaveformAnimator(count int) *waveformAnimator {
	if count <= 0 {
		count = barCount
	}
	a := &waveformAnimator{
		bars:   make([]float32, count),
		phases: make([]float64, count),
		freqs:  make([]float64, count),
	}
	center := float64(count-1) / 2
	for i := 0; i < count; i++ {
		dist := math.Abs(float64(i) - center)
		// Mirrored phases keep the capsule visually balanced while the
		// frequency spread stops the bars from moving in lockstep.
		a.phases[i] = dist * 0.62
		a.freqs[i] = 1.65 + 0.11*math.Mod(float64(i), 6)
	}
	return a
}

// Update advances the animation to `now` using the recorder's newest
// amplitude samples and returns the bar heights in [0,1].
//
// The returned slice is owned by the animator and is only valid until
// the next Update call.
func (a *waveformAnimator) Update(now time.Time, levels []float32) []float32 {
	if a.lastAt.IsZero() {
		a.lastAt = now.Add(-animatorFrame)
	}
	dt := now.Sub(a.lastAt)
	a.lastAt = now
	if dt <= 0 {
		dt = animatorFrame
	} else if dt > animatorMaxStep {
		dt = animatorMaxStep
	}
	dtSec := dt.Seconds()

	target := levelTarget(levels)
	tau := releaseTau
	if target > a.energy {
		tau = attackTau
	}
	a.energy += (target - a.energy) * (1 - math.Exp(-dtSec/tau))
	if a.energy < 1e-4 {
		a.energy = 0
	}
	a.clock += dtSec

	for i := range a.bars {
		want := a.barTarget(i)
		barTau := barReleaseTau
		if want > float64(a.bars[i]) {
			barTau = barAttackTau
		}
		cur := float64(a.bars[i])
		next := cur + (want-cur)*(1-math.Exp(-dtSec/barTau))
		if next < 0 {
			next = 0
		} else if next > 1 {
			next = 1
		}
		a.bars[i] = float32(next)
	}

	// Snap the tail so a silent capsule renders exactly flat and can be
	// skipped by the redraw dedupe.
	if a.Settled() {
		a.energy = 0
		for i := range a.bars {
			a.bars[i] = 0
		}
	}
	return a.bars
}

// barTarget is the height this bar wants at the current clock time.
func (a *waveformAnimator) barTarget(i int) float64 {
	center := float64(len(a.bars)-1) / 2
	dist := 0.0
	if center > 0 {
		dist = math.Abs(float64(i)-center) / center
	}
	// Centre-weighted profile: middle bars lead, outer bars follow at
	// roughly half amplitude.
	shape := 1 - 0.45*dist*dist

	t := a.clock
	wave := math.Sin(2*math.Pi*a.freqs[i]*t + a.phases[i])
	shimmer := math.Sin(2*math.Pi*(4.1+0.15*float64(i%3))*t + dist*2.4)
	osc := 0.60 + 0.27*wave + 0.16*shimmer
	if osc < 0.12 {
		osc = 0.12
	}
	return a.energy * shape * osc
}

// Settled reports whether the animation has collapsed to a flat line.
func (a *waveformAnimator) Settled() bool {
	if a.energy > 0.004 {
		return false
	}
	for _, v := range a.bars {
		if v > 0.01 {
			return false
		}
	}
	return true
}

// Reset flattens the animation; called when a session ends so the next
// recording starts from a flat line.
func (a *waveformAnimator) Reset() {
	a.energy = 0
	a.clock = 0
	a.lastAt = time.Time{}
	for i := range a.bars {
		a.bars[i] = 0
	}
}

// levelTarget maps raw peak amplitudes onto the 0..1 energy envelope.
// The newest sample, and the one before it, drive the envelope so short
// peaks do not flicker off between chunks.
func levelTarget(levels []float32) float64 {
	if len(levels) == 0 {
		return 0
	}
	newest := levels[len(levels)-1]
	if len(levels) >= 2 && levels[len(levels)-2] > newest {
		newest = levels[len(levels)-2]
	}
	v := (float64(newest) - silenceFloor) / (loudRef - silenceFloor)
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	return math.Sqrt(v)
}
