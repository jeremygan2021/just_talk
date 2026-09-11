package voice

import (
	"testing"
	"time"

	"github.com/c/just-talk-go/hotkey"
)

// TestTogglePressDuringStopDelayKeepsRecording is the regression test for
// the duplicate-transcript bug.
//
// In toggle mode the second hotkey press starts a stop delay (a short
// buffer so the last word is not clipped). Pressing again during that
// delay used to *commit* the in-flight session - dispatching its
// transcript - and then start a brand new recording, so one utterance
// could be pasted twice. The press must instead cancel the pending stop
// and keep the same session recording, exactly like hold mode.
func TestTogglePressDuringStopDelayKeepsRecording(t *testing.T) {
	p := testVoicePlugin()
	p.mode = "toggle"
	p.multiTapWindow = 0 // gesture detection off: exercise the toggle path only
	p.stopDelayMs = 800

	rec := NewRecorder(p.logger, 1)
	p.recorder = rec
	p.recording = true
	p.stopping = true
	p.userStopped = true
	p.stopAt = time.Now().Add(time.Second)
	p.stopTimer = time.AfterFunc(time.Hour, func() {})
	defer p.stopTimer.Stop()

	p.handleHotkey(hotkey.Event{Type: hotkey.KeyDown})

	p.mu.Lock()
	stopping, recording := p.stopping, p.recording
	pending, sameRecorder := p.pendingDone, p.recorder == rec
	p.mu.Unlock()

	if stopping {
		t.Fatal("a press during the stop delay must cancel the pending stop")
	}
	if !recording {
		t.Fatal("a press during the stop delay must keep recording")
	}
	if !sameRecorder {
		t.Fatal("a press during the stop delay must not detach the in-flight session")
	}
	if pending != 0 {
		t.Fatalf("a press during the stop delay committed the session: pendingDone = %d", pending)
	}
}

// TestTogglePressStartsStopDelay checks the normal second press still
// arms the stop delay.
func TestTogglePressStartsStopDelay(t *testing.T) {
	p := testVoicePlugin()
	p.mode = "toggle"
	p.multiTapWindow = 0
	p.stopDelayMs = 8000

	rec := NewRecorder(p.logger, 1)
	p.recorder = rec
	p.recording = true
	defer func() {
		p.mu.Lock()
		if p.stopTimer != nil {
			p.stopTimer.Stop()
			p.stopTimer = nil
		}
		p.mu.Unlock()
	}()

	p.handleHotkey(hotkey.Event{Type: hotkey.KeyDown})

	p.mu.Lock()
	stopping, userStopped := p.stopping, p.userStopped
	p.mu.Unlock()
	if !stopping {
		t.Fatal("second press must start the stop delay")
	}
	if !userStopped {
		t.Fatal("the session must be marked as user-stopped so its transcript is dispatched")
	}
}
