package overlay

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRenderPreviewPNG writes PNG contact sheets of the overlay artwork so
// the design can be reviewed without running the GUI. It is opt-in because
// it writes files:
//
//	JT_OVERLAY_PREVIEW=1 go test ./plugins/overlay/ -run TestRenderPreviewPNG -v
//
// JT_OVERLAY_PREVIEW_DIR overrides the output directory.
func TestRenderPreviewPNG(t *testing.T) {
	if os.Getenv("JT_OVERLAY_PREVIEW") == "" {
		t.Skip("set JT_OVERLAY_PREVIEW=1 to write preview PNGs")
	}
	dir := os.Getenv("JT_OVERLAY_PREVIEW_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "just-talk-overlay-preview")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	red := rgba{r: 255, g: 65, b: 65, a: 255}
	amber := rgba{r: 245, g: 190, b: 70, a: 255}
	stopping := rgba{r: 255, g: 160, b: 70, a: 255}
	green := rgba{r: 80, g: 210, b: 120, a: 255}
	grey := rgba{r: 145, g: 145, b: 145, a: 255}

	flat := make([]float32, barCount)
	quiet := make([]float32, barCount)
	mid := make([]float32, barCount)
	loud := make([]float32, barCount)
	for i := 0; i < barCount; i++ {
		centre := 1 - 0.45*float64(absInt(i-barCount/2))/float64(barCount/2)
		quiet[i] = float32(0.12 * centre)
		mid[i] = float32((0.35 + 0.25*float64(i%3)) * centre)
		loud[i] = float32((0.72 + 0.28*float64(i%2)) * centre)
	}

	states := []struct {
		label  string
		accent rgba
		bars   []float32
		phase  float64
	}{
		{"REC", red, quiet, 0.4},
		{"REC", red, mid, 1.1},
		{"REC", red, loud, 2.3},
		{"WAI", stopping, flat, 0.7},
		{"CON", amber, nil, 1.5},
		{"IDL", grey, nil, 2.1},
		{"ERR", red, nil, 0.0},
		{"⏎", green, nil, 0.9},
		{"↶", stopping, nil, 1.9},
	}

	panes := make([]*argbCanvas, 0, len(states))
	for _, st := range states {
		c := newTestCanvas(t)
		renderOverlay(c, st.label, st.accent, st.bars, 1, st.phase)
		panes = append(panes, c)
	}
	writeSheet(t, filepath.Join(dir, "overlay-states.png"), composeSheet(panes, 3, 2, 10))

	// Motion sheet: consecutive animation frames of one utterance so the
	// "call indicator" behaviour (react to audio, flatten in silence) can
	// be inspected frame by frame.
	animator := newWaveformAnimator(barCount)
	now := time.Unix(0, 0)
	motion := make([]*argbCanvas, 0, 8)
	levels := []float32{0.28, 0.04, 0.22, 0.30, 0.03, 0.26}
	for i := 0; i < 8; i++ {
		now = now.Add(33 * time.Millisecond)
		bars := animator.Update(now, []float32{levels[i%len(levels)]})
		c := newTestCanvas(t)
		renderOverlay(c, "REC", red, bars, 1, float64(i)*0.033)
		motion = append(motion, c)
	}
	writeSheet(t, filepath.Join(dir, "overlay-motion.png"), composeSheet(motion, 4, 2, 10))

	// Font sheet: every glyph the capsule can paint.
	fontLines := []string{
		"ABCDEFGHIJKLM",
		"NOPQRSTUVWXYZ",
		"0123456789",
		".,!?'\"-_:;()/+=",
		"%&*#@",
		"CON REC WAI STP",
		"ERR IDL 12:34",
	}
	writeSheet(t, filepath.Join(dir, "overlay-font.png"), composeTextSheet(fontLines))

	t.Logf("preview sheets written to %s", dir)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func writeSheet(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}

// composeSheet tiles overlay windows over a desktop-like backdrop, scaled
// up so the antialiasing and the glow are visible in the PNG.
func composeSheet(panes []*argbCanvas, cols, zoom, gap int) image.Image {
	winW, winH := overlayWindowSize(1)
	paneW, paneH := winW*zoom, winH*zoom
	rows := (len(panes) + cols - 1) / cols
	sheetW := cols*paneW + (cols+1)*gap
	sheetH := rows*paneH + (rows+1)*gap

	img := newBackdrop(sheetW, sheetH)
	for i, c := range panes {
		ox := gap + (i%cols)*(paneW+gap)
		oy := gap + (i/cols)*(paneH+gap)
		drawPaneOver(img, c, ox, oy, zoom)
	}
	return img
}

// composeTextSheet renders the bitmap font on a plain backdrop.
func composeTextSheet(lines []string) image.Image {
	const scale = 3
	lineH := 9 * scale
	pad := 4 * scale
	maxW := 0
	for _, l := range lines {
		if w := bitmapTextWidth(l, scale); w > maxW {
			maxW = w
		}
	}
	c := newARGBCanvas(maxW+2*pad, len(lines)*lineH+2*pad)
	for i, line := range lines {
		drawSurfaceText(c, pad, pad+i*lineH, line, scale, overlayTextRGBA)
	}
	img := newBackdrop(c.w, c.h)
	drawPaneOver(img, c, 0, 0, 1)
	return img
}

func newBackdrop(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	top := color.RGBA{R: 0x1c, G: 0x1e, B: 0x28, A: 0xff}
	bottom := color.RGBA{R: 0x0b, G: 0x0c, B: 0x12, A: 0xff}
	for y := 0; y < h; y++ {
		f := float64(y) / float64(h)
		row := color.RGBA{
			R: uint8(float64(top.R) + (float64(bottom.R)-float64(top.R))*f),
			G: uint8(float64(top.G) + (float64(bottom.G)-float64(top.G))*f),
			B: uint8(float64(top.B) + (float64(bottom.B)-float64(top.B))*f),
			A: 0xff,
		}
		draw.Draw(img, image.Rect(0, y, w, y+1), &image.Uniform{C: row}, image.Point{}, draw.Src)
	}
	return img
}

func drawPaneOver(img *image.RGBA, c *argbCanvas, ox, oy, zoom int) {
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			p := c.pixel(x, y)
			if p.a == 0 {
				continue
			}
			for zy := 0; zy < zoom; zy++ {
				for zx := 0; zx < zoom; zx++ {
					blendOver(img, ox+x*zoom+zx, oy+y*zoom+zy, p)
				}
			}
		}
	}
}

func blendOver(img *image.RGBA, x, y int, p rgba) {
	if !(image.Point{X: x, Y: y}).In(img.Bounds()) {
		return
	}
	i := img.PixOffset(x, y)
	a := float64(p.a) / 255
	img.Pix[i+0] = uint8(float64(p.r)*a + float64(img.Pix[i+0])*(1-a))
	img.Pix[i+1] = uint8(float64(p.g)*a + float64(img.Pix[i+1])*(1-a))
	img.Pix[i+2] = uint8(float64(p.b)*a + float64(img.Pix[i+2])*(1-a))
	img.Pix[i+3] = 0xff
}
