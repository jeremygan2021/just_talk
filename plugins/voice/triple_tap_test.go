package voice

import (
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// The voice plugin writes progress lines through the package-level output
	// writer; keep test output clean (TUI mode does the same in production).
	SetOutput(io.Discard)
	os.Exit(m.Run())
}

func testVoicePlugin() *VoicePlugin {
	return &VoicePlugin{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestTripleTapDetectedWhenIdle(t *testing.T) {
	p := testVoicePlugin()
	p.tripleTapSend = true
	p.tripleTapWindow = 500 * time.Millisecond
	base := time.Now()
	if got := p.noteTapLocked(base); got != tapNone {
		t.Fatal("first tap should not complete a gesture")
	}
	if got := p.noteTapLocked(base.Add(100 * time.Millisecond)); got != tapNone {
		t.Fatal("second tap should not complete a gesture")
	}
	if got := p.noteTapLocked(base.Add(200 * time.Millisecond)); got != tapTriple {
		t.Fatal("third tap should complete a triple tap")
	}
	if p.tapCount != 0 {
		t.Fatalf("tapCount = %d, want 0 after trigger", p.tapCount)
	}
}

func TestTripleTapContinuesAfterFirstTapStartsRecording(t *testing.T) {
	p := testVoicePlugin()
	p.tripleTapSend = true
	p.tripleTapWindow = 500 * time.Millisecond
	base := time.Now()
	p.noteTapLocked(base) // idle at sequence start
	p.recording = true    // the first tap started a recording
	p.noteTapLocked(base.Add(100 * time.Millisecond))
	if got := p.noteTapLocked(base.Add(200 * time.Millisecond)); got != tapTriple {
		t.Fatal("third tap should complete a gesture that started while idle")
	}
}

func TestTripleTapDisabled(t *testing.T) {
	p := testVoicePlugin()
	p.tripleTapWindow = 500 * time.Millisecond
	base := time.Now()
	for i := 0; i < 3; i++ {
		if got := p.noteTapLocked(base.Add(time.Duration(i) * 50 * time.Millisecond)); got != tapNone {
			t.Fatal("triple tap must not fire when disabled")
		}
	}
}

func TestTripleTapIgnoredWhileRecording(t *testing.T) {
	p := testVoicePlugin()
	p.tripleTapSend = true
	p.tripleTapWindow = 500 * time.Millisecond
	p.recording = true
	base := time.Now()
	for i := 0; i < 3; i++ {
		if got := p.noteTapLocked(base.Add(time.Duration(i) * 50 * time.Millisecond)); got != tapNone {
			t.Fatal("triple tap must not fire while recording")
		}
	}
	if p.tapCount != 0 {
		t.Fatalf("tapCount = %d, want 0 while recording", p.tapCount)
	}
}

func TestTripleTapIgnoredWhileFinishPending(t *testing.T) {
	p := testVoicePlugin()
	p.tripleTapSend = true
	p.tripleTapWindow = 500 * time.Millisecond
	p.pendingDone = 1
	base := time.Now()
	for i := 0; i < 3; i++ {
		if got := p.noteTapLocked(base.Add(time.Duration(i) * 50 * time.Millisecond)); got != tapNone {
			t.Fatal("triple tap must not fire while a finish is pending")
		}
	}
}

func TestTripleTapIgnoredDuringError(t *testing.T) {
	p := testVoicePlugin()
	p.tripleTapSend = true
	p.tripleTapWindow = 500 * time.Millisecond
	p.lastError = "boom"
	p.errorUntil = time.Now().Add(time.Second)
	base := time.Now()
	for i := 0; i < 3; i++ {
		if got := p.noteTapLocked(base.Add(time.Duration(i) * 50 * time.Millisecond)); got != tapNone {
			t.Fatal("triple tap must not fire while an error is active")
		}
	}
}

func TestTripleTapSlowGapResets(t *testing.T) {
	p := testVoicePlugin()
	p.tripleTapSend = true
	p.tripleTapWindow = 200 * time.Millisecond
	base := time.Now()
	p.noteTapLocked(base)                             // tap 1
	p.noteTapLocked(base.Add(100 * time.Millisecond)) // tap 2 (double is off, so no timer)
	if got := p.noteTapLocked(base.Add(500 * time.Millisecond)); got != tapNone {
		t.Fatal("slow third tap must not trigger")
	}
	if p.tapCount != 1 {
		t.Fatalf("tapCount = %d, want 1 after a slow re-press restarts the sequence", p.tapCount)
	}
}

func TestDoubleTapFiresUndo(t *testing.T) {
	p := testVoicePlugin()
	p.doubleTapUndo = true
	p.doubleTapAction = undoTapActionUndo
	p.tripleTapWindow = 40 * time.Millisecond

	origSend := sendUndoInput
	defer func() { sendUndoInput = origSend }()
	called := make(chan string, 1)
	sendUndoInput = func(action string, _ *slog.Logger) error {
		called <- action
		return nil
	}
	defer setTUIStatus(func(s *TUIVoiceStatus) { *s = TUIVoiceStatus{State: "idle", UpdatedAt: time.Now()} })

	base := time.Now()
	p.noteTapLocked(base)
	p.noteTapLocked(base.Add(10 * time.Millisecond))
	if p.doubleTimer == nil {
		t.Fatal("a double-tap should arm a confirm timer")
	}

	select {
	case action := <-called:
		if action != undoTapActionUndo {
			t.Fatalf("action = %q, want %q", action, undoTapActionUndo)
		}
	case <-time.After(time.Second):
		t.Fatal("double-tap undo did not fire")
	}
	if status := TUIStatus(); status.State != "undo" {
		t.Fatalf("state = %q, want undo", status.State)
	}
}

func TestDoubleTapDisabledDoesNotArm(t *testing.T) {
	p := testVoicePlugin()
	p.tripleTapSend = true
	p.tripleTapWindow = 500 * time.Millisecond
	base := time.Now()
	p.noteTapLocked(base)
	p.noteTapLocked(base.Add(50 * time.Millisecond))
	if p.doubleTimer != nil {
		t.Fatal("double tap must not arm a timer when disabled")
	}
}

func TestTripleTapCancelsArmedDouble(t *testing.T) {
	p := testVoicePlugin()
	p.doubleTapUndo = true
	p.tripleTapSend = true
	p.tripleTapWindow = 40 * time.Millisecond

	origSend := sendUndoInput
	defer func() { sendUndoInput = origSend }()
	sendUndoInput = func(string, *slog.Logger) error {
		t.Error("undo must not fire once a third tap completes the triple")
		return nil
	}

	base := time.Now()
	p.noteTapLocked(base)
	p.noteTapLocked(base.Add(10 * time.Millisecond))
	if got := p.noteTapLocked(base.Add(20 * time.Millisecond)); got != tapTriple {
		t.Fatal("third tap should complete a triple tap")
	}
	if p.doubleTimer != nil {
		t.Fatal("triple tap should cancel the armed double-tap timer")
	}
	time.Sleep(80 * time.Millisecond)
}

func TestFireDoubleTapIgnoresIncompleteSequence(t *testing.T) {
	p := testVoicePlugin()
	p.doubleTapUndo = true
	p.tripleTapWindow = 50 * time.Millisecond

	origSend := sendUndoInput
	defer func() { sendUndoInput = origSend }()
	sendUndoInput = func(string, *slog.Logger) error {
		t.Error("undo must not fire without two taps")
		return nil
	}

	p.tapCount = 1
	p.fireDoubleTap()
	if p.tapCount != 1 {
		t.Fatalf("tapCount = %d, want 1 (unchanged)", p.tapCount)
	}
}

func TestTriggerEnterSendSetsHintAndSends(t *testing.T) {
	origSend := sendEnterKey
	defer func() { sendEnterKey = origSend }()
	called := make(chan struct{}, 1)
	sendEnterKey = func(*slog.Logger) error {
		called <- struct{}{}
		return nil
	}
	defer setTUIStatus(func(s *TUIVoiceStatus) { *s = TUIVoiceStatus{State: "idle", UpdatedAt: time.Now()} })

	p := testVoicePlugin()
	p.triggerEnterSend()

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("SendEnter was not called")
	}

	status := TUIStatus()
	if status.State != "enter" {
		t.Fatalf("state = %q, want enter", status.State)
	}
	if status.EnterUntil.IsZero() {
		t.Fatal("EnterUntil should be set so the overlay shows the hint")
	}
}
