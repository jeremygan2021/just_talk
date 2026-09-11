package hotkey

import (
	"testing"
	"time"
)

func TestStandardComboKeyUpFiresWhenModifierReleasesFirst(t *testing.T) {
	tracker := NewKeyStateTracker()
	ch := make(chan Event, 4)
	combo := Combo{Mods: ModAlt, Key: KeyR}
	tracker.Watch(combo, ch)

	events := tracker.KeyDown(KeyAlt, time.Now())
	if len(events) != 0 {
		t.Fatalf("Alt down emitted %d events, want 0", len(events))
	}

	events = tracker.KeyDown(KeyR, time.Now())
	if len(events) != 1 || events[0].Combo != combo || events[0].Type != KeyDown {
		t.Fatalf("R down emitted %#v, want one KeyDown for %s", events, combo)
	}

	events = tracker.KeyUp(KeyAlt, time.Now())
	if len(events) != 1 || events[0].Combo != combo || events[0].Type != KeyUp {
		t.Fatalf("Alt up emitted %#v, want one KeyUp for %s", events, combo)
	}

	events = tracker.KeyUp(KeyR, time.Now())
	if len(events) != 0 {
		t.Fatalf("R up emitted %d events, want 0", len(events))
	}
}

func TestStandardComboKeyDownFiresWhenModifierPressedAfterKey(t *testing.T) {
	tracker := NewKeyStateTracker()
	ch := make(chan Event, 4)
	combo := Combo{Mods: ModAlt, Key: KeyR}
	tracker.Watch(combo, ch)

	events := tracker.KeyDown(KeyR, time.Now())
	if len(events) != 0 {
		t.Fatalf("R down emitted %d events, want 0", len(events))
	}

	events = tracker.KeyDown(KeyAlt, time.Now())
	if len(events) != 1 || events[0].Combo != combo || events[0].Type != KeyDown {
		t.Fatalf("Alt down emitted %#v, want one KeyDown for %s", events, combo)
	}

	events = tracker.KeyUp(KeyR, time.Now())
	if len(events) != 1 || events[0].Combo != combo || events[0].Type != KeyUp {
		t.Fatalf("R up emitted %#v, want one KeyUp for %s", events, combo)
	}

	events = tracker.KeyUp(KeyAlt, time.Now())
	if len(events) != 0 {
		t.Fatalf("Alt up emitted %d events, want 0", len(events))
	}
}

func TestCaptureEmitsModifierPlusKey(t *testing.T) {
	tracker := NewKeyStateTracker()
	capture := tracker.StartCapture()
	if capture == nil {
		t.Fatal("StartCapture returned nil")
	}
	defer tracker.StopCapture()

	tracker.KeyDown(KeyAlt, time.Now())
	tracker.KeyDown(KeyF8, time.Now())

	select {
	case combo := <-capture:
		if combo.Mods != ModAlt || combo.Key != KeyF8 {
			t.Fatalf("captured %s, want Alt+F8", combo)
		}
	case <-time.After(time.Second):
		t.Fatal("capture did not emit Alt+F8 within 1s")
	}
}

func TestCaptureEmitsBareModifier(t *testing.T) {
	tracker := NewKeyStateTracker()
	capture := tracker.StartCapture()
	defer tracker.StopCapture()

	tracker.KeyDown(KeyCtrl, time.Now())
	tracker.KeyUp(KeyCtrl, time.Now())

	select {
	case combo := <-capture:
		if combo.Mods != ModCtrl || combo.Key != KeyNone {
			t.Fatalf("captured %s, want Ctrl", combo)
		}
	case <-time.After(time.Second):
		t.Fatal("capture did not emit bare Ctrl within 1s")
	}
}

func TestCaptureSingleShot(t *testing.T) {
	tracker := NewKeyStateTracker()
	first := tracker.StartCapture()
	if first == nil {
		t.Fatal("first StartCapture returned nil")
	}
	if second := tracker.StartCapture(); second != nil {
		t.Fatal("second StartCapture should return nil while one is in flight")
	}
	tracker.StopCapture()
	if third := tracker.StartCapture(); third == nil {
		t.Fatal("StartCapture after Stop should succeed")
	}
	tracker.StopCapture()
	_ = first
}
