package voice

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestTripleTapDetectedWhenIdle(t *testing.T) {
	p := &VoicePlugin{tripleTapSend: true, tripleTapWindow: 500 * time.Millisecond}
	base := time.Now()
	if p.noteTapLocked(base) {
		t.Fatal("first tap should not complete a triple tap")
	}
	if p.noteTapLocked(base.Add(100 * time.Millisecond)) {
		t.Fatal("second tap should not complete a triple tap")
	}
	if !p.noteTapLocked(base.Add(200 * time.Millisecond)) {
		t.Fatal("third tap should complete a triple tap")
	}
	if p.tapCount != 0 {
		t.Fatalf("tapCount = %d, want 0 after trigger", p.tapCount)
	}
}

func TestTripleTapContinuesAfterFirstTapStartsRecording(t *testing.T) {
	p := &VoicePlugin{tripleTapSend: true, tripleTapWindow: 500 * time.Millisecond}
	base := time.Now()
	p.noteTapLocked(base) // idle at sequence start
	p.recording = true    // the first tap started a recording
	p.noteTapLocked(base.Add(100 * time.Millisecond))
	if !p.noteTapLocked(base.Add(200 * time.Millisecond)) {
		t.Fatal("third tap should complete a gesture that started while idle")
	}
}

func TestTripleTapDisabled(t *testing.T) {
	p := &VoicePlugin{tripleTapSend: false, tripleTapWindow: 500 * time.Millisecond}
	base := time.Now()
	for i := 0; i < 3; i++ {
		if p.noteTapLocked(base.Add(time.Duration(i) * 50 * time.Millisecond)) {
			t.Fatal("triple tap must not fire when disabled")
		}
	}
}

func TestTripleTapIgnoredWhileRecording(t *testing.T) {
	p := &VoicePlugin{tripleTapSend: true, tripleTapWindow: 500 * time.Millisecond, recording: true}
	base := time.Now()
	for i := 0; i < 3; i++ {
		if p.noteTapLocked(base.Add(time.Duration(i) * 50 * time.Millisecond)) {
			t.Fatal("triple tap must not fire while recording")
		}
	}
	if p.tapCount != 0 {
		t.Fatalf("tapCount = %d, want 0 while recording", p.tapCount)
	}
}

func TestTripleTapIgnoredWhileFinishPending(t *testing.T) {
	p := &VoicePlugin{tripleTapSend: true, tripleTapWindow: 500 * time.Millisecond, pendingDone: 1}
	base := time.Now()
	for i := 0; i < 3; i++ {
		if p.noteTapLocked(base.Add(time.Duration(i) * 50 * time.Millisecond)) {
			t.Fatal("triple tap must not fire while a finish is pending")
		}
	}
}

func TestTripleTapIgnoredDuringError(t *testing.T) {
	p := &VoicePlugin{
		tripleTapSend:   true,
		tripleTapWindow: 500 * time.Millisecond,
		lastError:       "boom",
		errorUntil:      time.Now().Add(time.Second),
	}
	base := time.Now()
	for i := 0; i < 3; i++ {
		if p.noteTapLocked(base.Add(time.Duration(i) * 50 * time.Millisecond)) {
			t.Fatal("triple tap must not fire while an error is active")
		}
	}
}

func TestTripleTapSlowGapResets(t *testing.T) {
	p := &VoicePlugin{tripleTapSend: true, tripleTapWindow: 200 * time.Millisecond}
	base := time.Now()
	p.noteTapLocked(base)                                  // tap 1
	p.noteTapLocked(base.Add(100 * time.Millisecond))      // tap 2
	if p.noteTapLocked(base.Add(500 * time.Millisecond)) { // too slow
		t.Fatal("slow third tap must not trigger")
	}
	if p.tapCount != 1 {
		t.Fatalf("tapCount = %d, want 1 after a slow re-press restarts the sequence", p.tapCount)
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

	SetOutput(io.Discard)
	p := &VoicePlugin{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
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

	setTUIStatus(func(s *TUIVoiceStatus) { *s = TUIVoiceStatus{State: "idle", UpdatedAt: time.Now()} })
}
