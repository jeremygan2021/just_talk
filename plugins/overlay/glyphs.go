package overlay

import "unicode/utf8"

// bitmapTextWidth returns the rendered pixel width of s at the given
// bitmap scale. Each rune advances the cursor by 6*scale pixels (see
// drawBitmapText / drawText in backend_x11.go and backend_wayland.go).
// Rune count, not byte count, is used so multi-byte symbols (for
// example the Return glyph) measure the same way drawText advances.
//
// Shared by the Wayland, X11, and stub backends so the overlay label
// truncation logic in overlay.go can reason about the same width the
// renderers will paint.
func bitmapTextWidth(s string, scale int) int {
	if len(s) == 0 {
		return 0
	}
	return (utf8.RuneCountInString(s)*6 - 1) * scale
}

// glyphs is the 5x7 pixel font used by the Linux backends. The map
// only contains the glyphs the overlay actually paints: the status
// abbreviations (CON, REC, WAI, STP, ERR, IDL), the multi-tap
// gesture arrows (⏎, ↶), and the digits used for the configuration
// UI. Anything outside the map advances the cursor by 4*scale (a
// narrow placeholder) — see drawBitmapText / drawText.
//
// Lives outside the platform-specific files so overlay.go can use it
// from any build tag combination.
var glyphs = map[rune][7]byte{
	'A': {0b01110, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001},
	'C': {0b01110, 0b10001, 0b10000, 0b10000, 0b10000, 0b10001, 0b01110},
	'D': {0b11110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b11110},
	'E': {0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b11111},
	'I': {0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b11111},
	'N': {0b10001, 0b11001, 0b10101, 0b10011, 0b10001, 0b10001, 0b10001},
	'O': {0b01110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110},
	'P': {0b11110, 0b10001, 0b10001, 0b11110, 0b10000, 0b10000, 0b10000},
	'R': {0b11110, 0b10001, 0b10001, 0b11110, 0b10100, 0b10010, 0b10001},
	'S': {0b01111, 0b10000, 0b10000, 0b01110, 0b00001, 0b00001, 0b11110},
	'T': {0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100},
	'W': {0b10001, 0b10001, 0b10001, 0b10101, 0b10101, 0b10101, 0b01010},
	'⏎': {0b00001, 0b00001, 0b00001, 0b00101, 0b11111, 0b00100, 0b00000},
	'↶': {0b00000, 0b00011, 0b00100, 0b01000, 0b11111, 0b01000, 0b00100},
}
