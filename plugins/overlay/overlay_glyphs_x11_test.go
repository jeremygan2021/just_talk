//go:build linux && !no_x11

package overlay

import "testing"

func TestGestureGlyphsAreDistinct(t *testing.T) {
	enter, ok := glyphs['⏎']
	if !ok {
		t.Fatal("missing return glyph")
	}
	undo, ok := glyphs['↶']
	if !ok {
		t.Fatal("missing retract glyph")
	}
	if enter == undo {
		t.Fatal("return and retract glyphs must differ")
	}
}
