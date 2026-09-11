package overlay

import (
	"math"
	"strings"
)

// This file holds the backend-agnostic overlay artwork. The Linux
// backends (X11 ARGB window, Wayland shm buffer) and the render tests
// all paint through the same primitives so the capsule looks identical
// everywhere. Platform files only own the window/surface plumbing and
// provide a `pillSurface` implementation.
//
// Layout summary (scale 1):
//
//	+--------------------------------------+
//	|          soft blue outer glow        |
//	|  +--------------------------------+  |
//	|  |  ● REC                         |  |  <- status row
//	|  |      ▁▃▅█▅▃▁  (live waveform)  |  |  <- waveform row
//	|  +--------------------------------+  |
//	+--------------------------------------+
//
// The recording/waiting states use the two-row layout; every other
// state keeps the compact one-row capsule.

// rgba is a straight (non-premultiplied) 8-bit color. Surfaces convert
// it to their own pixel layout while writing.
type rgba struct {
	r, g, b, a uint8
}

// pillSurface is the minimal pixel target the shared renderer needs.
type pillSurface interface {
	surfaceSize() (w, h int)
	// putPixel writes c ignoring what was there before. Callers use it
	// for opaque fills; the written bytes are premultiplied.
	putPixel(x, y int, c rgba)
	// blendPixel composites c with alpha c.a*coverage/255 over the
	// existing pixel using premultiplied math.
	blendPixel(x, y int, c rgba, coverage uint8)
}

// Geometry, all in unscaled pixels. Everything is multiplied by the
// configured overlay scale at layout time.
const (
	// glowPad is the transparent margin kept around the capsule. The
	// overlay window is widePill + 2*glowPad so the blue halo always has
	// room to bleed outside the rounded rectangle.
	glowPad = 14

	// Compact capsule used by every non-waveform state.
	narrowPillW = 150
	narrowPillH = 42

	// Two-row capsule used while recording/waiting for the transcript.
	widePillW = 240
	widePillH = 70

	dotDiameter = 14
	// labelScale is the bitmap-font scale. 3 gives the same 21px "REC"
	// the overlay has always shown, so the letters do not change size.
	labelScale = 3

	// Waveform geometry. The bars are mirrored around the vertical
	// centre of the waveform row, call-indicator style.
	barCount = 15
	barWidth = 4
	barGap   = 3
	// barMaxH is the full height (both halves) of a bar at level 1.
	barMaxH = 26
	// barMinH is the height of a resting bar: with no audio the row
	// collapses to a thin flat line.
	barMinH = 3

	widePadX      = 18
	widePadTop    = 9
	widePadBottom = 9
)

// Palette. The accent (dot) color still comes from the status, but the
// capsule chrome and the waveform are blue/cyan so the whole capsule
// has the subtle blue-glow look.
var (
	pillTopColor    = rgba{30, 34, 48, 236}
	pillBottomColor = rgba{11, 13, 20, 243}
	rimTopColor     = rgba{132, 190, 255, 205}
	rimBottomColor  = rgba{44, 78, 140, 175}
	glowTintColor   = rgba{72, 150, 255, 175}
	overlayTextRGBA = rgba{238, 244, 255, 255}
	barCoreColor    = rgba{92, 168, 255, 255}
	barTipColor     = rgba{152, 236, 255, 255}
	barGlowColor    = rgba{80, 190, 255, 255}
)

// overlayWindowSize returns the fixed overlay window size for a scale.
// The window never changes size; the capsule inside it shrinks for the
// compact states and grows for the waveform states.
func overlayWindowSize(scale float64) (int, int) {
	if scale <= 0 {
		scale = 1
	}
	return scaledValue(widePillW+2*glowPad, scale), scaledValue(widePillH+2*glowPad, scale)
}

// anchorMargin returns the screen margin the overlay window should keep
// so that the *capsule* (not the glow) sits `margin` pixels away from
// the screen edge.
func anchorMargin(margin int, scale float64) int {
	m := margin - scaledValue(glowPad, scale)
	if m < 0 {
		m = 0
	}
	return m
}

func scaledValue(v int, scale float64) int {
	n := int(float64(v)*scale + 0.5)
	if n < 1 {
		return 1
	}
	return n
}

// pillLayout carries every derived coordinate of one frame.
type pillLayout struct {
	scale      float64
	wide       bool
	winW, winH int

	pillX, pillY, pillW, pillH, radius int
	glowPad                            int

	dotX, dotY, dotSize int
	text                string
	textX, textY        int
	textScale, textH    int

	barsX, barsCenterY    int
	barW, barGap, barMaxH int
	barCount              int
}

// newPillLayout computes the frame geometry. `label` is clipped to the
// space available next to the dot and the clipped text is stored in
// l.text.
func newPillLayout(label string, wide bool, scale float64, winW, winH int) pillLayout {
	if scale <= 0 {
		scale = 1
	}
	l := pillLayout{scale: scale, wide: wide, winW: winW, winH: winH}

	l.pillW = scaledValue(narrowPillW, scale)
	l.pillH = scaledValue(narrowPillH, scale)
	if wide {
		l.pillW = scaledValue(widePillW, scale)
		l.pillH = scaledValue(widePillH, scale)
	}
	l.pillX = (winW - l.pillW) / 2
	l.pillY = (winH - l.pillH) / 2
	l.radius = l.pillH / 2
	l.glowPad = scaledValue(glowPad, scale)

	l.dotSize = scaledValue(dotDiameter, scale)
	l.textScale = scaledValue(labelScale, scale)
	l.textH = 7 * l.textScale
	gap := scaledValue(14, scale)

	if !wide {
		textW := bitmapTextWidth(label, l.textScale)
		contentW := l.dotSize + gap + textW
		minX := l.pillX + scaledValue(8, scale)
		x := l.pillX + (l.pillW-contentW)/2
		if x < minX {
			x = minX
		}
		l.dotX = x
		l.dotY = l.pillY + (l.pillH-l.dotSize)/2
		l.text = label
		l.textX = x + l.dotSize + gap
		l.textY = l.pillY + (l.pillH-l.textH)/2
		return l
	}

	innerX := scaledValue(widePadX, scale)
	avail := l.pillW - 2*innerX - l.dotSize - gap
	if avail < 0 {
		avail = 0
	}
	l.text = clipLabel(label, l.textScale, avail)

	textW := bitmapTextWidth(l.text, l.textScale)
	rowW := l.dotSize + gap + textW
	rowX := l.pillX + (l.pillW-rowW)/2
	rowTop := l.pillY + scaledValue(widePadTop, scale)

	l.dotX = rowX
	l.dotY = rowTop + (l.textH-l.dotSize)/2
	l.textX = rowX + l.dotSize + gap
	l.textY = rowTop

	l.barW = scaledValue(barWidth, scale)
	l.barGap = scaledValue(barGap, scale)
	l.barMaxH = scaledValue(barMaxH, scale)
	l.barCount = barCount
	barsW := l.barCount*l.barW + (l.barCount-1)*l.barGap
	l.barsX = l.pillX + (l.pillW-barsW)/2
	barsBottom := l.pillY + l.pillH - scaledValue(widePadBottom, scale)
	l.barsCenterY = barsBottom - l.barMaxH/2
	return l
}

// renderOverlay paints one frame.
//
//	accent: status color of the round indicator
//	bars:   animated bar heights in [0,1]; empty ⇒ compact layout
//	phase:  animation clock in seconds (breathing glow / indicator pulse)
func renderOverlay(s pillSurface, label string, accent rgba, bars []float32, scale float64, phase float64) {
	winW, winH := s.surfaceSize()
	l := newPillLayout(label, len(bars) > 0, scale, winW, winH)

	level := float64(maxBarValue(bars))
	breath := 0.5 + 0.5*math.Sin(2*math.Pi*phase/2.8)

	glow := 0.34 + 0.16*breath
	if l.wide {
		glow += 0.62 * level
	}
	if glow > 1 {
		glow = 1
	}

	fillOuterGlow(s, l, glow)
	fillPill(s, l)
	strokePill(s, l, 0.55+0.35*level+0.12*breath)

	// Round status indicator with a soft halo that swells with the audio.
	cx := float64(l.dotX) + float64(l.dotSize)/2
	cy := float64(l.dotY) + float64(l.dotSize)/2
	r := float64(l.dotSize) / 2
	haloR := r + float64(scaledValue(3, scale))*(1+2.4*level)
	haloA := uint8(50 + 85*level)
	fillCircleShape(s, cx, cy, haloR, rgba{accent.r, accent.g, accent.b, haloA}, true)
	fillCircleShape(s, cx, cy, r, accent, false)

	if l.text != "" {
		drawSurfaceText(s, l.textX, l.textY, l.text, l.textScale, overlayTextRGBA)
	}
	if l.wide {
		drawBars(s, l, bars)
	}
}

// drawBars paints the mirrored call-indicator waveform inside the
// waveform row.
func drawBars(s pillSurface, l pillLayout, bars []float32) {
	minHalf := float64(scaledValue(barMinH, l.scale)) / 2
	maxHalf := float64(l.barMaxH) / 2
	glowExtra := float64(scaledValue(2, l.scale))
	cy := float64(l.barsCenterY)
	radius := float64(l.barW) / 2

	for i := 0; i < l.barCount; i++ {
		v := float64(0)
		if i < len(bars) {
			v = float64(bars[i])
		}
		if v < 0 {
			v = 0
		} else if v > 1 {
			v = 1
		}
		// Resting bars keep a thin sliver so the row reads as a flat
		// line when nobody is speaking.
		half := minHalf + (maxHalf-minHalf)*v
		x := float64(l.barsX+i*(l.barW+l.barGap)) + radius

		glowA := uint8(26 + 96*v)
		fillCapsule(s, x, cy-half, x, cy+half, radius+glowExtra,
			rgba{barGlowColor.r, barGlowColor.g, barGlowColor.b, glowA},
			rgba{barGlowColor.r, barGlowColor.g, barGlowColor.b, glowA}, true)
		fillCapsule(s, x, cy-half, x, cy+half, radius, barCoreColor, barTipColor, false)
	}
}

// fillOuterGlow paints the soft blue halo around the capsule. Only the
// ring between the capsule and the window border is touched.
func fillOuterGlow(s pillSurface, l pillLayout, strength float64) {
	pad := float64(l.glowPad)
	if pad <= 0 || strength <= 0 {
		return
	}
	minX := l.pillX - l.glowPad
	maxX := l.pillX + l.pillW + l.glowPad
	minY := l.pillY - l.glowPad
	maxY := l.pillY + l.pillH + l.glowPad
	base := float64(glowTintColor.a) * strength

	for py := minY; py <= maxY; py++ {
		for px := minX; px <= maxX; px++ {
			d := sdRoundRect(float64(px)+0.5, float64(py)+0.5,
				float64(l.pillX), float64(l.pillY), float64(l.pillW), float64(l.pillH), float64(l.radius))
			if d < 0 || d >= pad {
				continue
			}
			t := 1 - d/pad
			a := base * t * t
			if a < 1 {
				continue
			}
			if a > 232 {
				a = 232
			}
			s.blendPixel(px, py, rgba{glowTintColor.r, glowTintColor.g, glowTintColor.b, uint8(a)}, 255)
		}
	}
}

func fillPill(s pillSurface, l pillLayout) {
	h := float64(l.pillH - 1)
	if h <= 0 {
		h = 1
	}
	for py := l.pillY; py < l.pillY+l.pillH; py++ {
		t := float64(py-l.pillY) / h
		base := lerpRGBA(pillTopColor, pillBottomColor, t)
		for px := l.pillX; px < l.pillX+l.pillW; px++ {
			d := sdRoundRect(float64(px)+0.5, float64(py)+0.5,
				float64(l.pillX), float64(l.pillY), float64(l.pillW), float64(l.pillH), float64(l.radius))
			if d > 0.5 {
				continue
			}
			if d <= -0.5 {
				s.putPixel(px, py, base)
				continue
			}
			s.blendPixel(px, py, base, uint8((0.5-d)*255))
		}
	}
}

// strokePill draws the 1px blue rim that gives the capsule its edge
// light. `strength` is driven by the audio level so the rim brightens
// while the user speaks.
func strokePill(s pillSurface, l pillLayout, strength float64) {
	if strength <= 0 {
		return
	}
	if strength > 1 {
		strength = 1
	}
	w := l.scale
	if w < 1 {
		w = 1
	}
	h := float64(l.pillH - 1)
	if h <= 0 {
		h = 1
	}
	for py := l.pillY - 1; py <= l.pillY+l.pillH; py++ {
		for px := l.pillX - 1; px <= l.pillX+l.pillW; px++ {
			d := math.Abs(sdRoundRect(float64(px)+0.5, float64(py)+0.5,
				float64(l.pillX), float64(l.pillY), float64(l.pillW), float64(l.pillH), float64(l.radius)))
			if d > w {
				continue
			}
			cov := 1 - d/w
			t := float64(py-l.pillY) / h
			c := lerpRGBA(rimTopColor, rimBottomColor, t)
			a := float64(c.a) * cov * strength
			if a < 1 {
				continue
			}
			s.blendPixel(px, py, rgba{c.r, c.g, c.b, uint8(a)}, 255)
		}
	}
}

func maxBarValue(bars []float32) float32 {
	var m float32
	for _, v := range bars {
		if v > m {
			m = v
		}
	}
	return m
}

func lerpRGBA(a, b rgba, t float64) rgba {
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	lerp := func(x, y uint8) uint8 {
		v := float64(x) + (float64(y)-float64(x))*t
		if v < 0 {
			v = 0
		} else if v > 255 {
			v = 255
		}
		return uint8(v + 0.5)
	}
	return rgba{lerp(a.r, b.r), lerp(a.g, b.g), lerp(a.b, b.b), lerp(a.a, b.a)}
}

// sdRoundRect returns the signed distance from (px,py) to the rounded
// rectangle. Negative inside, positive outside, |d| < 0.5 at the edge.
func sdRoundRect(px, py, x, y, w, h, r float64) float64 {
	if w <= 0 || h <= 0 {
		return 1e9
	}
	if r > w/2 {
		r = w / 2
	}
	if r > h/2 {
		r = h / 2
	}
	cx := x + w/2
	cy := y + h/2
	qx := math.Abs(px-cx) - (w/2 - r)
	qy := math.Abs(py-cy) - (h/2 - r)
	ax := math.Max(qx, 0)
	ay := math.Max(qy, 0)
	return math.Hypot(ax, ay) + math.Min(math.Max(qx, qy), 0) - r
}

// sdCapsule returns the distance from (px,py) to the segment (ax,ay)-(bx,by).
func sdCapsule(px, py, ax, ay, bx, by float64) float64 {
	pax, pay := px-ax, py-ay
	bax, bay := bx-ax, by-ay
	den := bax*bax + bay*bay
	t := 0.0
	if den > 0 {
		t = (pax*bax + pay*bay) / den
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
	}
	return math.Hypot(pax-bax*t, pay-bay*t)
}

// fillCircleShape paints a filled circle. When blend is false the
// interior is written opaquely (a solid dot); when true the circle is
// composited, so c.a acts as the halo strength.
func fillCircleShape(s pillSurface, cx, cy, r float64, c rgba, blend bool) {
	if r <= 0 {
		return
	}
	minX := int(math.Floor(cx - r - 1))
	maxX := int(math.Ceil(cx + r + 1))
	minY := int(math.Floor(cy - r - 1))
	maxY := int(math.Ceil(cy + r + 1))
	for py := minY; py <= maxY; py++ {
		for px := minX; px <= maxX; px++ {
			d := math.Hypot(float64(px)+0.5-cx, float64(py)+0.5-cy) - r
			if d > 0.5 {
				continue
			}
			if !blend && d <= -0.5 {
				s.putPixel(px, py, c)
				continue
			}
			cov := uint8(255)
			if d > -0.5 {
				cov = uint8((0.5 - d) * 255)
			}
			s.blendPixel(px, py, c, cov)
		}
	}
}

// fillCapsule paints a rounded bar between two points. The color runs
// from `core` at the middle to `tip` at both ends, which makes loud bars
// glow cyan at their tips.
func fillCapsule(s pillSurface, x0, y0, x1, y1, r float64, core, tip rgba, blend bool) {
	if r <= 0 {
		return
	}
	minX := int(math.Floor(math.Min(x0, x1) - r - 1))
	maxX := int(math.Ceil(math.Max(x0, x1) + r + 1))
	minY := int(math.Floor(math.Min(y0, y1) - r - 1))
	maxY := int(math.Ceil(math.Max(y0, y1) + r + 1))
	midY := (y0 + y1) / 2
	half := math.Abs(y1-y0) / 2

	for py := minY; py <= maxY; py++ {
		for px := minX; px <= maxX; px++ {
			fx := float64(px) + 0.5
			fy := float64(py) + 0.5
			d := sdCapsule(fx, fy, x0, y0, x1, y1) - r
			if d > 0.5 {
				continue
			}
			c := core
			if half > 0.5 {
				c = lerpRGBA(core, tip, math.Abs(fy-midY)/half)
			}
			if !blend && d <= -0.5 {
				s.putPixel(px, py, c)
				continue
			}
			cov := uint8(255)
			if d > -0.5 {
				cov = uint8((0.5 - d) * 255)
			}
			s.blendPixel(px, py, c, cov)
		}
	}
}

// drawSurfaceText paints the 5x7 bitmap font used by every backend.
func drawSurfaceText(s pillSurface, x, y int, text string, scale int, c rgba) {
	for _, r := range strings.ToUpper(text) {
		glyph, ok := glyphs[r]
		if !ok {
			x += 4 * scale
			continue
		}
		for row, bits := range glyph {
			for col := 0; col < 5; col++ {
				if bits&(1<<(4-col)) == 0 {
					continue
				}
				for yy := 0; yy < scale; yy++ {
					for xx := 0; xx < scale; xx++ {
						s.putPixel(x+col*scale+xx, y+row*scale+yy, c)
					}
				}
			}
		}
		x += 6 * scale
	}
}

// argbCanvas is a premultiplied BGRA pixel buffer, the pixel format
// shared by XPutImage on a 32-bit ARGB visual and Wayland
// WL_SHM_FORMAT_ARGB8888. It is backend independent on purpose: the
// render tests use it too.
type argbCanvas struct {
	w, h int
	data []byte
}

func newARGBCanvas(w, h int) *argbCanvas {
	return &argbCanvas{w: w, h: h, data: make([]byte, w*h*4)}
}

func (p *argbCanvas) surfaceSize() (int, int) { return p.w, p.h }

func (p *argbCanvas) clear() {
	clear(p.data)
}

func (p *argbCanvas) putPixel(x, y int, c rgba) {
	if x < 0 || y < 0 || x >= p.w || y >= p.h {
		return
	}
	i := (y*p.w + x) * 4
	a := uint16(c.a)
	p.data[i+0] = uint8(uint16(c.b) * a / 255)
	p.data[i+1] = uint8(uint16(c.g) * a / 255)
	p.data[i+2] = uint8(uint16(c.r) * a / 255)
	p.data[i+3] = c.a
}

func (p *argbCanvas) blendPixel(x, y int, c rgba, coverage uint8) {
	if x < 0 || y < 0 || x >= p.w || y >= p.h || coverage == 0 {
		return
	}
	i := (y*p.w + x) * 4
	srcA := uint16(c.a) * uint16(coverage) / 255
	inv := 255 - srcA
	p.data[i+0] = uint8((uint16(c.b)*srcA + uint16(p.data[i+0])*inv) / 255)
	p.data[i+1] = uint8((uint16(c.g)*srcA + uint16(p.data[i+1])*inv) / 255)
	p.data[i+2] = uint8((uint16(c.r)*srcA + uint16(p.data[i+2])*inv) / 255)
	p.data[i+3] = uint8(srcA + uint16(p.data[i+3])*inv/255)
}

// pixel returns the straight (non-premultiplied) color at x,y.
func (p *argbCanvas) pixel(x, y int) rgba {
	if x < 0 || y < 0 || x >= p.w || y >= p.h {
		return rgba{}
	}
	i := (y*p.w + x) * 4
	a := uint16(p.data[i+3])
	if a == 0 {
		return rgba{}
	}
	return rgba{
		r: uint8(uint16(p.data[i+2]) * 255 / a),
		g: uint8(uint16(p.data[i+1]) * 255 / a),
		b: uint8(uint16(p.data[i+0]) * 255 / a),
		a: uint8(a),
	}
}
