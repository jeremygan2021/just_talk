//go:build linux && !no_x11

package overlay

// #cgo LDFLAGS: -lX11 -lXext -lXinerama -lXrender
// #include <X11/Xlib.h>
// #include <X11/Xatom.h>
// #include <X11/Xutil.h>
// #include <X11/extensions/Xinerama.h>
// #include <X11/extensions/Xrender.h>
// #include <X11/extensions/shape.h>
// #include <stdlib.h>
// #include <stdio.h>
//
// static void set_override_redirect(Display *dpy, Window win) {
//     XSetWindowAttributes attrs;
//     attrs.override_redirect = True;
//     attrs.save_under = True;
//     XChangeWindowAttributes(dpy, win, CWOverrideRedirect | CWSaveUnder, &attrs);
// }
//
// static void set_empty_input_shape(Display *dpy, Window win) {
//     XShapeCombineRectangles(dpy, win, ShapeInput, 0, 0, NULL, 0, ShapeSet, Unsorted);
// }
//
// static void set_window_type_utility(Display *dpy, Window win) {
//     Atom type = XInternAtom(dpy, "_NET_WM_WINDOW_TYPE", False);
//     Atom utility = XInternAtom(dpy, "_NET_WM_WINDOW_TYPE_UTILITY", False);
//     XChangeProperty(dpy, win, type, XA_ATOM, 32, PropModeReplace, (unsigned char*)&utility, 1);
// }
//
// static int focused_monitor_geometry(Display *dpy, int screen, int *x, int *y, int *w, int *h) {
//     Window focus;
//     int revert;
//     XGetInputFocus(dpy, &focus, &revert);
//     if (focus == None || focus == PointerRoot) return 0;
//
//     XWindowAttributes attrs;
//     if (!XGetWindowAttributes(dpy, focus, &attrs)) return 0;
//     Window child;
//     int wx = 0, wy = 0;
//     if (!XTranslateCoordinates(dpy, focus, RootWindow(dpy, screen), attrs.width / 2, attrs.height / 2, &wx, &wy, &child)) return 0;
//
//     int count = 0;
//     XineramaScreenInfo *screens = XineramaQueryScreens(dpy, &count);
//     if (screens == NULL || count <= 0) {
//         if (screens != NULL) XFree(screens);
//         return 0;
//     }
//     for (int i = 0; i < count; i++) {
//         int sx = screens[i].x_org;
//         int sy = screens[i].y_org;
//         int sw = screens[i].width;
//         int sh = screens[i].height;
//         if (wx >= sx && wx < sx + sw && wy >= sy && wy < sy + sh) {
//             *x = sx; *y = sy; *w = sw; *h = sh;
//             XFree(screens);
//             return 1;
//         }
//     }
//     XFree(screens);
//     return 0;
// }
//
// static Visual *find_argb_visual(Display *dpy, int screen) {
//     XVisualInfo template;
//     template.screen = screen;
//     template.depth = 32;
//     template.class = TrueColor;
//     int n = 0;
//     XVisualInfo *infos = XGetVisualInfo(dpy, VisualScreenMask | VisualDepthMask | VisualClassMask, &template, &n);
//     if (infos == NULL) return NULL;
//     Visual *visual = NULL;
//     for (int i = 0; i < n; i++) {
//         XRenderPictFormat *fmt = XRenderFindVisualFormat(dpy, infos[i].visual);
//         if (fmt != NULL && fmt->type == PictTypeDirect && fmt->direct.alphaMask) {
//             visual = infos[i].visual;
//             break;
//         }
//     }
//     XFree(infos);
//     return visual;
// }
//
// static XImage *create_argb_image(Display *dpy, Visual *visual, int w, int h, char *data) {
//     return XCreateImage(dpy, visual, 32, ZPixmap, 0, data, (unsigned int)w, (unsigned int)h, 32, 0);
// }
//
// static void destroy_ximage(XImage *img) {
//     XDestroyImage(img);
// }
//
// static int compositing_manager_running(Display *dpy, int screen) {
//     char name[32];
//     snprintf(name, sizeof(name), "_NET_WM_CM_S%d", screen);
//     Atom atom = XInternAtom(dpy, name, False);
//     Window owner = XGetSelectionOwner(dpy, atom);
//     return owner != None;
// }
import "C"

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"github.com/c/just-talk-go/config"
)

const (
	// baseMargin is the distance between the capsule (not the glow
	// around it) and the screen edge.
	baseMargin = 28
)

type x11Backend struct {
	dpy       *C.Display
	win       C.Window
	gc        C.GC
	screen    C.int
	visual    *C.Visual
	cmap      C.Colormap
	mask      C.Pixmap
	depth     C.int
	argb      bool
	visible   bool
	position  string
	scale     float64
	w         int
	h         int
	margin    int
	canvas    *argbCanvas
	shapeSet  bool
	shapeWide bool
	moved     bool
	lastX     int
	lastY     int
}

func newX11Backend(cfg config.OverlayConfig) (backend, error) {
	if os.Getenv("DISPLAY") == "" {
		return nil, fmt.Errorf("DISPLAY is not set")
	}
	dpy := C.XOpenDisplay(nil)
	if dpy == nil {
		return nil, fmt.Errorf("cannot open X display")
	}
	scale := cfg.Scale
	if scale <= 0 {
		scale = 1.0
	}
	b := &x11Backend{dpy: dpy, screen: C.XDefaultScreen(dpy), position: cfg.Position, scale: scale}
	b.w, b.h = overlayWindowSize(scale)
	b.margin = anchorMargin(scaledValue(baseMargin, scale), scale)
	if b.position == "" {
		b.position = "top-right"
	}
	root := C.XRootWindow(dpy, b.screen)
	b.argb = C.compositing_manager_running(dpy, b.screen) != 0
	if b.argb {
		b.visual = C.find_argb_visual(dpy, b.screen)
		if b.visual == nil {
			b.argb = false
		}
	}
	if b.argb {
		b.depth = 32
		b.cmap = C.XCreateColormap(dpy, root, b.visual, C.AllocNone)
	} else {
		b.depth = C.XDefaultDepth(dpy, b.screen)
		b.visual = C.XDefaultVisual(dpy, b.screen)
		b.cmap = C.XDefaultColormap(dpy, b.screen)
	}
	attrs := C.XSetWindowAttributes{
		colormap:          b.cmap,
		override_redirect: C.True,
		save_under:        C.True,
		background_pixel:  0,
		border_pixel:      0,
	}
	b.win = C.XCreateWindow(
		dpy,
		root,
		0, 0,
		C.uint(b.w), C.uint(b.h),
		0,
		b.depth,
		C.InputOutput,
		b.visual,
		C.CWColormap|C.CWOverrideRedirect|C.CWSaveUnder|C.CWBackPixel|C.CWBorderPixel,
		&attrs,
	)
	if b.win == 0 {
		C.XCloseDisplay(dpy)
		return nil, fmt.Errorf("cannot create overlay window")
	}
	b.canvas = newARGBCanvas(b.w, b.h)
	if !b.argb {
		C.set_override_redirect(dpy, b.win)
		b.applyShape(false)
	}
	C.set_window_type_utility(dpy, b.win)
	b.gc = C.XCreateGC(dpy, C.Drawable(b.win), 0, nil)
	C.XSelectInput(dpy, b.win, 0)
	C.set_empty_input_shape(dpy, b.win)
	b.move()
	C.XFlush(dpy)
	return b, nil
}

func (b *x11Backend) Show(f overlayFrame) error {
	if b.dpy == nil {
		return nil
	}
	b.move()
	b.draw(f)
	if !b.visible {
		C.XMapRaised(b.dpy, b.win)
		b.visible = true
	} else {
		C.XRaiseWindow(b.dpy, b.win)
	}
	C.XFlush(b.dpy)
	return nil
}

func (b *x11Backend) Hide() error {
	if b.dpy == nil || !b.visible {
		return nil
	}
	C.XUnmapWindow(b.dpy, b.win)
	C.XFlush(b.dpy)
	b.visible = false
	return nil
}

func (b *x11Backend) Close() error {
	if b.dpy == nil {
		return nil
	}
	if b.mask != 0 {
		C.XFreePixmap(b.dpy, b.mask)
	}
	if b.gc != nil {
		C.XFreeGC(b.dpy, b.gc)
	}
	if b.win != 0 {
		C.XDestroyWindow(b.dpy, b.win)
	}
	if b.argb && b.cmap != 0 {
		C.XFreeColormap(b.dpy, b.cmap)
	}
	C.XCloseDisplay(b.dpy)
	b.dpy = nil
	return nil
}

func (b *x11Backend) move() {
	monX, monY, monW, monH := b.focusedMonitor()
	x, y := monX+monW-b.w-b.margin, monY+b.margin
	switch strings.ToLower(b.position) {
	case "top-left":
		x, y = monX+b.margin, monY+b.margin
	case "top-center":
		x, y = monX+(monW-b.w)/2, monY+b.margin
	case "bottom-left":
		x, y = monX+b.margin, monY+monH-b.h-b.margin
	case "bottom-center":
		x, y = monX+(monW-b.w)/2, monY+monH-b.h-b.margin
	case "bottom-right":
		x, y = monX+monW-b.w-b.margin, monY+monH-b.h-b.margin
	}
	if x < monX {
		x = monX
	}
	if y < monY {
		y = monY
	}
	// The capsule is now a fixed-size window, so the animated frames keep
	// the same coordinates; only talk to the X server when it really
	// moves (the overlay follows the focused monitor).
	if b.moved && x == b.lastX && y == b.lastY {
		return
	}
	b.moved, b.lastX, b.lastY = true, x, y
	C.XMoveWindow(b.dpy, b.win, C.int(x), C.int(y))
}

func (b *x11Backend) focusedMonitor() (x, y, w, h int) {
	w = int(C.XDisplayWidth(b.dpy, b.screen))
	h = int(C.XDisplayHeight(b.dpy, b.screen))
	var cx, cy, cw, ch C.int
	if C.focused_monitor_geometry(b.dpy, b.screen, &cx, &cy, &cw, &ch) != 0 {
		return int(cx), int(cy), int(cw), int(ch)
	}
	return 0, 0, w, h
}

func (b *x11Backend) draw(f overlayFrame) {
	if !b.argb {
		b.drawShape(f)
		return
	}
	b.canvas.clear()
	renderOverlay(b.canvas, f.label, frameAccent(f.accent), f.bars, b.scale, f.phase)
	b.putImage(b.canvas.data)
}

// drawShape renders without a compositing manager: no alpha, no glow,
// just the capsule clipped to a rounded mask. Every color comes from
// the default colormap.
func (b *x11Backend) drawShape(f overlayFrame) {
	wide := len(f.bars) > 0
	if !b.shapeSet || b.shapeWide != wide {
		b.applyShape(wide)
	}

	l := newPillLayout(f.label, wide, b.scale, b.w, b.h)
	bg := b.alloc(22<<8, 25<<8, 36<<8)
	fg := b.alloc(238<<8, 244<<8, 255<<8)
	dot := b.alloc(f.accent.R, f.accent.G, f.accent.B)
	bar := b.alloc(120<<8, 210<<8, 255<<8)

	// Clear the whole window, not just the capsule: the window keeps its
	// size across states, and the mask can grow later, which would
	// otherwise expose stale pixels from the previous (wider) capsule.
	C.XSetForeground(b.dpy, b.gc, bg)
	C.XFillRectangle(b.dpy, C.Drawable(b.win), b.gc, 0, 0, C.uint(b.w), C.uint(b.h))

	C.XSetForeground(b.dpy, b.gc, dot)
	C.XFillArc(b.dpy, C.Drawable(b.win), b.gc, C.int(l.dotX), C.int(l.dotY), C.uint(l.dotSize), C.uint(l.dotSize), 0, 360*64)

	if l.text != "" {
		C.XSetForeground(b.dpy, b.gc, fg)
		drawBitmapText(b, l.textX, l.textY, l.text, l.textScale)
	}

	if wide {
		C.XSetForeground(b.dpy, b.gc, bar)
		minHalf := scaledValue(barMinH, b.scale) / 2
		maxHalf := l.barMaxH / 2
		radius := l.barW / 2
		for i := 0; i < l.barCount; i++ {
			v := float64(0)
			if i < len(f.bars) {
				v = float64(f.bars[i])
			}
			if v < 0 {
				v = 0
			} else if v > 1 {
				v = 1
			}
			half := minHalf + int(float64(maxHalf-minHalf)*v+0.5)
			x := l.barsX + i*(l.barW+l.barGap)
			top := l.barsCenterY - half
			C.XFillRectangle(b.dpy, C.Drawable(b.win), b.gc, C.int(x), C.int(top), C.uint(l.barW), C.uint(2*half))
			if radius > 0 {
				C.XFillArc(b.dpy, C.Drawable(b.win), b.gc, C.int(x), C.int(top), C.uint(l.barW), C.uint(l.barW), 0, 360*64)
				C.XFillArc(b.dpy, C.Drawable(b.win), b.gc, C.int(x), C.int(top+2*half-l.barW), C.uint(l.barW), C.uint(l.barW), 0, 360*64)
			}
		}
	}
}

func (b *x11Backend) alloc(r, g, bl uint16) C.ulong {
	cmap := C.XDefaultColormap(b.dpy, b.screen)
	color := C.XColor{red: C.ushort(r), green: C.ushort(g), blue: C.ushort(bl)}
	if C.XAllocColor(b.dpy, cmap, &color) == 0 {
		return C.XWhitePixel(b.dpy, b.screen)
	}
	return color.pixel
}

// applyShape clips the window to the capsule, which is what draws the
// rounded corners when no compositing manager is available.
func (b *x11Backend) applyShape(wide bool) {
	if b.mask != 0 {
		C.XFreePixmap(b.dpy, b.mask)
		b.mask = 0
	}
	l := newPillLayout("", wide, b.scale, b.w, b.h)
	mask := pillMask(b.w, b.h, l.pillX, l.pillY, l.pillW, l.pillH, l.radius)
	b.mask = C.XCreateBitmapFromData(b.dpy, C.Drawable(b.win), (*C.char)(unsafe.Pointer(&mask[0])), C.uint(b.w), C.uint(b.h))
	b.shapeSet = true
	b.shapeWide = wide
	if b.mask == 0 {
		return
	}
	C.XShapeCombineMask(b.dpy, b.win, C.ShapeBounding, 0, 0, b.mask, C.ShapeSet)
}

func (b *x11Backend) putImage(data []byte) {
	if len(data) == 0 {
		return
	}
	ptr := C.CBytes(data)
	img := C.create_argb_image(b.dpy, b.visual, C.int(b.w), C.int(b.h), (*C.char)(ptr))
	if img == nil {
		C.free(ptr)
		return
	}
	C.XPutImage(b.dpy, C.Drawable(b.win), b.gc, img, 0, 0, 0, 0, C.uint(b.w), C.uint(b.h))
	img.data = nil
	C.destroy_ximage(img)
	C.free(ptr)
}

// frameAccent converts a status color into the shared render color.
func frameAccent(c statusColor) rgba {
	return rgba{r: uint8(c.R >> 8), g: uint8(c.G >> 8), b: uint8(c.B >> 8), a: 255}
}

// pillMask builds a window-sized 1-bit mask with the capsule area set.
func pillMask(w, h, x, y, pw, ph, r int) []byte {
	stride := (w + 7) / 8
	data := make([]byte, stride*h)
	for py := y; py < y+ph; py++ {
		if py < 0 || py >= h {
			continue
		}
		for px := x; px < x+pw; px++ {
			if px < 0 || px >= w {
				continue
			}
			if sdRoundRect(float64(px)+0.5, float64(py)+0.5,
				float64(x), float64(y), float64(pw), float64(ph), float64(r)) <= 0 {
				data[py*stride+px/8] |= 1 << uint(px%8)
			}
		}
	}
	return data
}

func drawBitmapText(b *x11Backend, x, y int, s string, scale int) {
	for _, r := range strings.ToUpper(s) {
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
				C.XFillRectangle(
					b.dpy,
					C.Drawable(b.win),
					b.gc,
					C.int(x+col*scale),
					C.int(y+row*scale),
					C.uint(scale),
					C.uint(scale),
				)
			}
		}
		x += 6 * scale
	}
}
