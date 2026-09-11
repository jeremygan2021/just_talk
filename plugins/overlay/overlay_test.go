package overlay

import (
	"testing"

	"github.com/c/just-talk-go/plugins/voice"
)

func TestDisplayForEnterState(t *testing.T) {
	label, _, visible := displayForStatus(voice.TUIVoiceStatus{State: "enter"}, false)
	if !visible {
		t.Fatal("enter state must be visible even when idle is hidden")
	}
	if label != "⏎" {
		t.Fatalf("label = %q, want the return glyph", label)
	}
}

func TestDisplayForUndoState(t *testing.T) {
	label, _, visible := displayForStatus(voice.TUIVoiceStatus{State: "undo"}, false)
	if !visible {
		t.Fatal("undo state must be visible even when idle is hidden")
	}
	if label != "↶" {
		t.Fatalf("label = %q, want the retract glyph", label)
	}
}

func TestDisplayForIdleHidden(t *testing.T) {
	_, _, visible := displayForStatus(voice.TUIVoiceStatus{State: "idle"}, false)
	if visible {
		t.Fatal("idle state must stay hidden when idle_visible is false")
	}
}
