package overlay

import (
	"testing"
)

func newTestCanvas(t *testing.T) *argbCanvas {
	t.Helper()
	w, h := overlayWindowSize(1)
	return newARGBCanvas(w, h)
}

func TestOverlayWindowLeavesRoomForGlow(t *testing.T) {
	w, h := overlayWindowSize(1)
	if w != widePillW+2*glowPad {
		t.Fatalf("window width = %d, want %d", w, widePillW+2*glowPad)
	}
	if h != widePillH+2*glowPad {
		t.Fatalf("window height = %d, want %d", h, widePillH+2*glowPad)
	}
}

func TestAnchorMarginKeepsPillAtMargin(t *testing.T) {
	// The window is anchored `glowPad` closer to the screen edge, so the
	// capsule itself ends up exactly `margin` away.
	if got := anchorMargin(28, 1); got != 28-glowPad {
		t.Fatalf("anchorMargin(28, 1) = %d, want %d", got, 28-glowPad)
	}
	if got := anchorMargin(5, 1); got != 0 {
		t.Fatalf("anchorMargin(5, 1) = %d, want 0 (never negative)", got)
	}
}

func TestWideLayoutPutsWaveformBelowLabel(t *testing.T) {
	w, h := overlayWindowSize(1)
	l := newPillLayout("REC", true, 1, w, h)

	if !l.wide {
		t.Fatal("layout should be wide when bars are present")
	}
	labelBottom := l.textY + l.textH
	barsTop := l.barsCenterY - l.barMaxH/2
	if barsTop < labelBottom {
		t.Fatalf("waveform top (%d) must be below the label bottom (%d)", barsTop, labelBottom)
	}
	// The waveform row must stay inside the capsule.
	if l.barsCenterY+l.barMaxH/2 > l.pillY+l.pillH {
		t.Fatal("waveform overflows the capsule")
	}
	// And it must be big enough to be the visual focus: taller than the
	// label and made of chunky bars.
	if l.barMaxH <= l.textH {
		t.Fatalf("waveform height %d should exceed the label height %d", l.barMaxH, l.textH)
	}
	if l.barW < 3 {
		t.Fatalf("bar width = %d, want >= 3px so the waveform reads clearly", l.barW)
	}
	barsW := l.barCount*l.barW + (l.barCount-1)*l.barGap
	if barsW > l.pillW {
		t.Fatalf("waveform width %d overflows the capsule width %d", barsW, l.pillW)
	}

	// Centre-weighted symmetric rendering: the first and last bars share a
	// baseline, the row is centred in the capsule.
	firstX := l.barsX
	lastX := l.barsX + barsW
	if gapL, gapR := firstX-l.pillX, l.pillX+l.pillW-lastX; gapL-gapR > 2 || gapR-gapL > 2 {
		t.Fatalf("waveform not centred: left gap %d, right gap %d", gapL, gapR)
	}
}

func TestCompactLayoutIsCentredSingleRow(t *testing.T) {
	w, h := overlayWindowSize(1)
	l := newPillLayout("WAI", false, 1, w, h)

	if l.wide {
		t.Fatal("layout without bars must be compact")
	}
	if l.pillW != narrowPillW || l.pillH != narrowPillH {
		t.Fatalf("compact pill = %dx%d, want %dx%d", l.pillW, l.pillH, narrowPillW, narrowPillH)
	}
	if l.pillX != (w-narrowPillW)/2 || l.pillY != (h-narrowPillH)/2 {
		t.Fatal("compact capsule must be centred inside the overlay window")
	}
	if l.barCount != 0 {
		t.Fatal("compact layout must not reserve a waveform row")
	}
	contentW := l.dotSize + (l.textX - l.dotX - l.dotSize) + bitmapTextWidth(l.text, l.textScale)
	if got := l.pillX + (l.pillW-contentW)/2; l.dotX-got > 2 {
		t.Fatalf("dot+label group not centred: dotX %d, want about %d", l.dotX, got)
	}
}

func TestClipLabel(t *testing.T) {
	scale := 3
	if got := clipLabel("REC", scale, 1000); got != "REC" {
		t.Fatalf("clipLabel kept %q, want the whole label", got)
	}
	// Two runes fit in 12*scale pixels.
	if got := clipLabel("ABCDE", scale, 12*scale); got != "AB" {
		t.Fatalf("clipLabel = %q, want %q", got, "AB")
	}
	if got := clipLabel("ABCDE", scale, 1); got != "" {
		t.Fatalf("clipLabel with no room = %q, want empty", got)
	}
}

// countPixels returns how many pixels satisfy pred.
func countPixels(c *argbCanvas, pred func(rgba) bool) int {
	n := 0
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			if pred(c.pixel(x, y)) {
				n++
			}
		}
	}
	return n
}

func barsFor(levels ...float32) []float32 {
	out := make([]float32, barCount)
	for i := range out {
		out[i] = levels[i%len(levels)]
	}
	return out
}

func TestRenderRecordingFrame(t *testing.T) {
	c := newTestCanvas(t)
	accent := rgba{r: 255, g: 65, b: 65, a: 255}
	renderOverlay(c, "REC", accent, barsFor(0.2, 0.5, 0.9, 0.6), 1, 0)

	w, h := overlayWindowSize(1)
	l := newPillLayout("REC", true, 1, w, h)

	// The capsule itself is an opaque, dark, slightly blue panel.
	mid := c.pixel(l.pillX+l.pillW/2, l.pillY+4)
	if mid.a < 200 {
		t.Fatalf("capsule alpha = %d, want an opaque panel", mid.a)
	}
	if int(mid.b) <= int(mid.r) {
		t.Fatalf("capsule should be blue-tinted, got %+v", mid)
	}

	// The status dot keeps the status color.
	dot := c.pixel(l.dotX+l.dotSize/2, l.dotY+l.dotSize/2)
	if dot.r < 200 || dot.g > 140 || dot.b > 140 {
		t.Fatalf("recording dot = %+v, want the red accent", dot)
	}

	// The waveform row is where the bright cyan bars live.
	rowTop := l.barsCenterY - l.barMaxH/2
	rowBottom := l.barsCenterY + l.barMaxH/2
	cyan := 0
	for y := rowTop; y <= rowBottom; y++ {
		for x := l.barsX; x < l.barsX+l.barCount*(l.barW+l.barGap); x++ {
			p := c.pixel(x, y)
			if p.a > 150 && p.b > 200 && p.g > 140 && p.r < 220 {
				cyan++
			}
		}
	}
	if cyan == 0 {
		t.Fatal("no cyan waveform pixels found in the waveform row")
	}

	// The blue halo bleeds outside the capsule.
	glowY := l.pillY - glowPad/2
	glow := c.pixel(l.pillX+l.pillW/2, glowY)
	if glow.a == 0 {
		t.Fatal("expected a glow outside the capsule")
	}
	if int(glow.b) <= int(glow.r) {
		t.Fatalf("glow should be blue, got %+v", glow)
	}

	// Far away from the capsule the window stays transparent, so the
	// overlay never paints a rectangle over the desktop.
	if corner := c.pixel(0, 0); corner.a != 0 {
		t.Fatalf("window corner should be transparent, got %+v", corner)
	}
}

func TestRenderLabelRowHasText(t *testing.T) {
	c := newTestCanvas(t)
	renderOverlay(c, "REC", rgba{r: 255, g: 65, b: 65, a: 255}, barsFor(0.5), 1, 0)

	w, h := overlayWindowSize(1)
	l := newPillLayout("REC", true, 1, w, h)

	white := 0
	for y := l.textY; y < l.textY+l.textH; y++ {
		for x := l.textX; x < l.textX+bitmapTextWidth("REC", l.textScale); x++ {
			p := c.pixel(x, y)
			if p.a > 200 && p.r > 200 && p.g > 200 && p.b > 200 {
				white++
			}
		}
	}
	if white == 0 {
		t.Fatal("label row should contain white glyph pixels")
	}
}

// TestRenderBarsGrowWithLevel checks the waveform actually responds to the
// level: a loud frame must light up far more of the waveform row than a
// silent one (which stays a flat line of stubs).
func TestRenderBarsGrowWithLevel(t *testing.T) {
	w, h := overlayWindowSize(1)
	l := newPillLayout("REC", true, 1, w, h)
	rowTop := l.barsCenterY - l.barMaxH/2
	rowBottom := l.barsCenterY + l.barMaxH/2

	countLit := func(levels ...float32) int {
		c := newTestCanvas(t)
		renderOverlay(c, "REC", rgba{r: 255, g: 65, b: 65, a: 255}, barsFor(levels...), 1, 0)
		n := 0
		for y := rowTop; y <= rowBottom; y++ {
			for x := l.barsX; x < l.barsX+l.barCount*(l.barW+l.barGap); x++ {
				// Bars are the only bright blue/cyan pixels in the row;
				// the capsule behind them is dark.
				p := c.pixel(x, y)
				if p.b > 150 && p.g > 110 {
					n++
				}
			}
		}
		return n
	}

	quiet := countLit(0)
	loud := countLit(1.0)
	if loud <= quiet {
		t.Fatalf("loud frame should cover more of the waveform row: quiet=%d loud=%d", quiet, loud)
	}
	if quiet == 0 {
		t.Fatal("silent bars should still render a flat baseline")
	}
}

func TestRenderCompactFrameHasNoWaveformRow(t *testing.T) {
	c := newTestCanvas(t)
	renderOverlay(c, "WAI", rgba{r: 255, g: 160, b: 70, a: 255}, nil, 1, 0)

	w, h := overlayWindowSize(1)
	l := newPillLayout("WAI", false, 1, w, h)

	// The compact capsule is narrower, so the wide capsule's right edge
	// must stay transparent.
	if p := c.pixel(l.pillX+l.pillW+glowPad, l.pillY+l.pillH/2); p.a != 0 {
		t.Fatalf("compact frame painted outside the narrow capsule: %+v", p)
	}
	// No waveform row exists below the capsule content.
	cyan := countPixels(c, func(p rgba) bool { return p.a > 150 && p.b > 200 && p.g > 150 && p.r < 200 })
	if cyan != 0 {
		t.Fatalf("compact frame should not contain waveform bars, found %d cyan pixels", cyan)
	}
}

func TestRenderScaleTwoKeepsProportions(t *testing.T) {
	w, h := overlayWindowSize(2)
	c := newARGBCanvas(w, h)
	renderOverlay(c, "REC", rgba{r: 255, g: 65, b: 65, a: 255}, barsFor(0.8), 2, 0.4)

	l := newPillLayout("REC", true, 2, w, h)
	if l.pillW != 2*widePillW || l.pillH != 2*widePillH {
		t.Fatalf("scaled capsule = %dx%d, want %dx%d", l.pillW, l.pillH, 2*widePillW, 2*widePillH)
	}
	if p := c.pixel(l.pillX+l.pillW/2, l.pillY+l.pillH/2); p.a < 200 {
		t.Fatalf("scaled capsule centre not painted: %+v", p)
	}
}

func TestRenderEmptyLabelStillDrawsCapsule(t *testing.T) {
	c := newTestCanvas(t)
	renderOverlay(c, "", rgba{r: 145, g: 145, b: 145, a: 255}, nil, 1, 0)
	l := newPillLayout("", false, 1, c.w, c.h)
	if p := c.pixel(l.pillX+l.pillW/2, l.pillY+l.pillH/2); p.a < 200 {
		t.Fatalf("capsule should render even without a label: %+v", p)
	}
}
