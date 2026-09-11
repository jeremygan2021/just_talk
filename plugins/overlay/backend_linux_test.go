//go:build linux && !no_x11

package overlay

import (
	"os"
	"testing"

	"github.com/c/just-talk-go/config"
)

// These tests exercise the real Linux backends against the running display
// server. They draw into the window/surface buffer *without* attaching or
// mapping it, so nothing appears on screen, but the pixel path
// (premultiplied ARGB, capsule geometry, glow, bars) is the production one.
//
// They skip themselves when the matching display server is unavailable.

func testFrame() overlayFrame {
	return overlayFrame{
		label:  "REC",
		accent: statusColor{R: 255 << 8, G: 65 << 8, B: 65 << 8},
		bars:   barsFor(0.2, 0.6, 0.95, 0.7),
		phase:  1.2,
	}
}

func checkCapsulePixels(t *testing.T, pixel func(x, y int) rgba) {
	t.Helper()
	w, h := overlayWindowSize(1)
	l := newPillLayout("REC", true, 1, w, h)

	if p := pixel(0, 0); p.a != 0 {
		t.Fatalf("window corner should stay transparent, got %+v", p)
	}
	centre := pixel(l.pillX+l.pillW/2, l.pillY+4)
	if centre.a < 200 {
		t.Fatalf("capsule centre = %+v, want an opaque panel", centre)
	}
	if int(centre.b) <= int(centre.r) {
		t.Fatalf("capsule should be blue-tinted, got %+v", centre)
	}
	dot := pixel(l.dotX+l.dotSize/2, l.dotY+l.dotSize/2)
	if dot.r < 200 || dot.g > 140 || dot.b > 140 {
		t.Fatalf("status dot = %+v, want the red accent", dot)
	}
	glow := pixel(l.pillX+l.pillW/2, l.pillY-glowPad/2)
	if glow.a == 0 || int(glow.b) <= int(glow.r) {
		t.Fatalf("expected a blue glow outside the capsule, got %+v", glow)
	}
	bars := 0
	for y := l.barsCenterY - l.barMaxH/2; y <= l.barsCenterY+l.barMaxH/2; y++ {
		for x := l.barsX; x < l.barsX+l.barCount*(l.barW+l.barGap); x++ {
			p := pixel(x, y)
			if p.b > 150 && p.g > 110 {
				bars++
			}
		}
	}
	if bars == 0 {
		t.Fatal("expected cyan waveform pixels in the waveform row")
	}
}

func TestWaylandBackendDrawsShmBuffer(t *testing.T) {
	if os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("XDG_SESSION_TYPE") != "wayland" {
		t.Skip("no Wayland session")
	}
	cfg := config.OverlayConfig{Enabled: true, Position: "bottom-center", Scale: 1}
	b, err := newWaylandBackend(cfg)
	if err != nil {
		t.Skipf("wayland backend unavailable: %v", err)
	}
	defer b.Close()

	wb, ok := b.(*waylandBackend)
	if !ok {
		t.Fatalf("unexpected backend type %T", b)
	}
	// Draw without attaching the buffer: the surface stays unmapped.
	wb.draw(testFrame())

	checkCapsulePixels(t, func(x, y int) rgba {
		if x < 0 || y < 0 || x >= wb.w || y >= wb.h {
			return rgba{}
		}
		i := (y*wb.w + x) * 4
		a := uint16(wb.data[i+3])
		if a == 0 {
			return rgba{}
		}
		return rgba{
			r: uint8(uint16(wb.data[i+2]) * 255 / a),
			g: uint8(uint16(wb.data[i+1]) * 255 / a),
			b: uint8(uint16(wb.data[i+0]) * 255 / a),
			a: uint8(a),
		}
	})
}

func TestX11BackendDrawsArgbCanvas(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no X display")
	}
	cfg := config.OverlayConfig{Enabled: true, Position: "top-right", Scale: 1}
	b, err := newX11Backend(cfg)
	if err != nil {
		t.Skipf("X11 backend unavailable: %v", err)
	}
	defer b.Close()

	xb, ok := b.(*x11Backend)
	if !ok {
		t.Fatalf("unexpected backend type %T", b)
	}
	if !xb.argb {
		t.Skip("no compositing manager: X11 falls back to the shaped window path")
	}
	// Draw into the canvas and upload it to the still-unmapped window.
	xb.draw(testFrame())
	checkCapsulePixels(t, xb.canvas.pixel)
}

// TestPillMaskShape covers the no-compositor fallback: the window is
// clipped to a rounded capsule mask, so the mask must be set inside the
// capsule, clear at the corners and clear outside it.
func TestPillMaskShape(t *testing.T) {
	w, h := overlayWindowSize(1)
	l := newPillLayout("REC", true, 1, w, h)
	mask := pillMask(w, h, l.pillX, l.pillY, l.pillW, l.pillH, l.radius)
	stride := (w + 7) / 8
	set := func(x, y int) bool { return mask[y*stride+x/8]&(1<<uint(x%8)) != 0 }

	if !set(l.pillX+l.pillW/2, l.pillY+l.pillH/2) {
		t.Fatal("capsule centre must be inside the mask")
	}
	if !set(l.pillX+l.pillW/2, l.pillY+1) {
		t.Fatal("capsule top edge (mid-width) must be inside the mask")
	}
	if set(l.pillX+1, l.pillY+1) {
		t.Fatal("rounded corner must be masked off")
	}
	if set(0, 0) || set(w-1, h-1) {
		t.Fatal("window corners must be masked off")
	}
	if set(l.pillX-1, l.pillY+l.pillH/2) {
		t.Fatal("glow padding must be masked off")
	}
}

// TestX11ShapedFallbackDraws runs the non-composited drawing path (plain
// XFillRectangle/XFillArc calls on a shaped window) to make sure it does
// not panic and still takes the shared layout.
func TestX11ShapedFallbackDraws(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no X display")
	}
	cfg := config.OverlayConfig{Enabled: true, Position: "top-right", Scale: 1}
	b, err := newX11Backend(cfg)
	if err != nil {
		t.Skipf("X11 backend unavailable: %v", err)
	}
	defer b.Close()
	xb, ok := b.(*x11Backend)
	if !ok {
		t.Fatalf("unexpected backend type %T", b)
	}
	xb.drawShape(testFrame())
	xb.drawShape(overlayFrame{label: "WAI", accent: statusColor{R: 255 << 8, G: 160 << 8}})
	if !xb.shapeSet {
		t.Fatal("the shaped path must install a window mask")
	}
}
