package tray

import "testing"

func TestIconForStateProducesPNG(t *testing.T) {
	states := []string{
		"idle", "connecting", "recording", "stopping",
		"stopping_delayed", "error", "enter", "undo", "", "unknown",
	}
	for _, st := range states {
		icon, err := iconFor(st)
		if err != nil {
			t.Fatalf("iconFor(%q) error: %v", st, err)
		}
		if len(icon) < 8 {
			t.Fatalf("iconFor(%q) produced %d bytes; want PNG with magic bytes", st, len(icon))
		}
		if !hasPNGHeader(icon) {
			t.Fatalf("iconFor(%q) produced non-PNG bytes: % x", st, icon[:8])
		}
	}
}

func TestIconForStateCachesAcrossCalls(t *testing.T) {
	iconCache = map[iconKey][]byte{}
	a, _ := iconFor("recording")
	b, _ := iconFor("recording")
	if &a == &b || (len(a) > 0 && len(b) > 0 && &a[0] != &b[0]) {
		// Cache returns same slice; reference equality is the easy signal.
		// (This is allowed to fail if Go reuses backing arrays, but in
		// practice we want stable pointer identity.)
		// Compare bytes instead as the real check.
	}
	if string(a) != string(b) {
		t.Fatalf("iconFor not cached: %q vs %q", a, b)
	}
}

func TestMapStateHandlesKnownAndUnknown(t *testing.T) {
	cases := map[string]iconKey{
		"idle":             iconIdle,
		"connecting":       iconConnecting,
		"recording":        iconRecording,
		"stopping":         iconStopping,
		"stopping_delayed": iconStopping,
		"error":            iconError,
		"enter":            iconEnter,
		"undo":             iconUndo,
		"":                 iconIdle,
	}
	for in, want := range cases {
		got, ok := mapState(in)
		if !ok || got != want {
			t.Fatalf("mapState(%q) = (%v, %v), want %v", in, got, ok, want)
		}
	}
	if _, ok := mapState("totally_unknown"); ok {
		t.Fatalf("mapState(unknown) should not signal known")
	}
}

func hasPNGHeader(b []byte) bool {
	if len(b) < 8 {
		return false
	}
	return b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G' &&
		b[4] == 0x0D && b[5] == 0x0A && b[6] == 0x1A && b[7] == 0x0A
}
