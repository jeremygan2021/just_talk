package overlay

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/c/just-talk-go/plugins/voice"
)

func TestDisplayForEnterState(t *testing.T) {
	label, _, _, visible := displayForStatus(voice.TUIVoiceStatus{State: "enter"}, false)
	if !visible {
		t.Fatal("enter state must be visible even when idle is hidden")
	}
	if label != "⏎" {
		t.Fatalf("label = %q, want the return glyph", label)
	}
}

func TestDisplayForUndoState(t *testing.T) {
	label, _, _, visible := displayForStatus(voice.TUIVoiceStatus{State: "undo"}, false)
	if !visible {
		t.Fatal("undo state must be visible even when idle is hidden")
	}
	if label != "↶" {
		t.Fatalf("label = %q, want the retract glyph", label)
	}
}

func TestDisplayForIdleHidden(t *testing.T) {
	_, _, _, visible := displayForStatus(voice.TUIVoiceStatus{State: "idle"}, false)
	if visible {
		t.Fatal("idle state must stay hidden when idle_visible is false")
	}
}

func TestDisplayForRecordingWithoutPartial(t *testing.T) {
	label, _, levels, visible := displayForStatus(voice.TUIVoiceStatus{State: "recording"}, false)
	if !visible {
		t.Fatal("recording must be visible")
	}
	if label != "REC" {
		t.Fatalf("label = %q, want %q", label, "REC")
	}
	if levels != nil {
		t.Fatalf("levels should be nil when recorder has no samples, got %v", levels)
	}
}

func TestDisplayForRecordingWithPartial(t *testing.T) {
	status := voice.TUIVoiceStatus{
		State:        "recording",
		PartialText:  "你好世界",
		LevelSamples: []float32{0.1, 0.2, 0.3},
	}
	label, _, levels, visible := displayForStatus(status, false)
	if !visible {
		t.Fatal("recording must be visible")
	}
	if label != "你好世界" {
		t.Fatalf("label = %q, want %q", label, "你好世界")
	}
	if len(levels) != 3 {
		t.Fatalf("levels = %v, want len 3", levels)
	}
}

// TestStatusLabelsAreDrawable guards the label font: every rune of every
// status label the overlay can show must exist in the bitmap font,
// otherwise the capsule renders a blank gap (the old font was missing
// "L", so IDL showed as "ID").
func TestStatusLabelsAreDrawable(t *testing.T) {
	labels := []string{"CON", "REC", "WAI", "STP", "ERR", "IDL", "⏎", "↶", "12:34"}
	for _, label := range labels {
		for _, r := range label {
			if _, ok := glyphs[r]; !ok {
				t.Errorf("label %q needs a glyph for %q", label, r)
			}
		}
	}
}

func TestTrimPartial(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"hello", "hello"},
		{"hello world  ", "hello world"},
		{strings.Repeat("a", partialTextMax+10), strings.Repeat("a", partialTextMax)},
		{"中文句子" + strings.Repeat("啊", 100), strings.Repeat("啊", partialTextMax)},
	}
	for _, tc := range cases {
		got := trimPartial(tc.in)
		if got != tc.want {
			t.Errorf("trimPartial(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWaveformState(t *testing.T) {
	wide := map[string]bool{
		"recording":        true,
		"stopping":         true,
		"stopping_delayed": true,
		"connecting":       false,
		"enter":            false,
		"undo":             false,
		"error":            false,
		"idle":             false,
	}
	for state, want := range wide {
		if got := waveformState(state); got != want {
			t.Errorf("waveformState(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestAnimatedState(t *testing.T) {
	animated := map[string]bool{
		"recording":        true,
		"stopping":         true,
		"stopping_delayed": true,
		"connecting":       true,
		"enter":            true,
		"undo":             true,
		"error":            false,
		"idle":             false,
	}
	for state, want := range animated {
		if got := animatedState(state); got != want {
			t.Errorf("animatedState(%q) = %v, want %v", state, got, want)
		}
	}
}

// fakeBackend records the frames the plugin asks it to render.
type fakeBackend struct {
	frames []overlayFrame
	hides  int
}

func (f *fakeBackend) Show(frame overlayFrame) error {
	cp := frame
	cp.bars = append([]float32(nil), frame.bars...)
	f.frames = append(f.frames, cp)
	return nil
}

func (f *fakeBackend) Hide() error  { f.hides++; return nil }
func (f *fakeBackend) Close() error { return nil }

func newTestPlugin() (*Plugin, *fakeBackend) {
	fb := &fakeBackend{}
	return &Plugin{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		backend:  fb,
		animator: newWaveformAnimator(barCount),
	}, fb
}

func TestSyncRecordingRendersWaveform(t *testing.T) {
	p, fb := newTestPlugin()
	now := time.Now()

	p.sync(voice.TUIVoiceStatus{State: "recording", LevelSamples: []float32{0.3}}, now, 0)
	if len(fb.frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(fb.frames))
	}
	frame := fb.frames[0]
	if frame.label != "REC" {
		t.Fatalf("label = %q, want REC", frame.label)
	}
	if len(frame.bars) != barCount {
		t.Fatalf("bars = %d, want %d", len(frame.bars), barCount)
	}
	if maxBarValue(frame.bars) <= 0.05 {
		t.Fatalf("speech should raise the bars, got %v", frame.bars)
	}
}

// TestSyncRecordingWithoutLevelsStillWide pins the layout: the waveform
// row is present for the whole recording, so the capsule does not jump
// between one-row and two-row shapes mid-session.
func TestSyncRecordingWithoutLevelsStillWide(t *testing.T) {
	p, fb := newTestPlugin()
	p.sync(voice.TUIVoiceStatus{State: "recording"}, time.Now(), 0)
	if len(fb.frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(fb.frames))
	}
	if len(fb.frames[0].bars) != barCount {
		t.Fatalf("bars = %d, want a flat %d-bar row", len(fb.frames[0].bars), barCount)
	}
	if maxBarValue(fb.frames[0].bars) != 0 {
		t.Fatal("a recording without samples must render a flat waveform")
	}
}

func TestSyncStoppingDecaysThenFlattens(t *testing.T) {
	p, fb := newTestPlugin()
	now := time.Now()
	for i := 0; i < 10; i++ {
		now = now.Add(overlayFrameInterval)
		p.sync(voice.TUIVoiceStatus{State: "recording", LevelSamples: []float32{0.3}}, now, 0)
	}
	if maxBarValue(p.animator.bars) < 0.2 {
		t.Fatal("expected loud bars while recording")
	}

	// The final wait keeps the waveform row, but the bars must ease flat.
	now = now.Add(overlayFrameInterval)
	p.sync(voice.TUIVoiceStatus{State: "stopping"}, now, 0)
	if len(fb.frames) < 2 {
		t.Fatal("stopping must still render a frame")
	}
	last := fb.frames[len(fb.frames)-1]
	if len(last.bars) != barCount {
		t.Fatal("stopping must keep the two-row layout")
	}

	for i := 0; i < 40; i++ {
		now = now.Add(overlayFrameInterval)
		p.sync(voice.TUIVoiceStatus{State: "stopping"}, now, 0)
	}
	if maxBarValue(p.animator.bars) != 0 {
		t.Fatalf("bars must flatten while waiting for the transcript, got %v", p.animator.bars)
	}
}

func TestSyncIdleHidesAndResetsAnimator(t *testing.T) {
	p, fb := newTestPlugin()
	now := time.Now()
	p.sync(voice.TUIVoiceStatus{State: "recording", LevelSamples: []float32{0.3}}, now, 0)
	now = now.Add(overlayFrameInterval)
	p.sync(voice.TUIVoiceStatus{State: "idle"}, now, 0)

	if fb.hides != 1 {
		t.Fatalf("hides = %d, want 1", fb.hides)
	}
	if !p.animator.Settled() {
		t.Fatal("leaving the waveform states must reset the animator")
	}
}

func TestSyncStaticStateIsNotRedrawn(t *testing.T) {
	p, fb := newTestPlugin()
	p.cfg.IdleVisible = true
	now := time.Now()
	p.sync(voice.TUIVoiceStatus{State: "idle"}, now, 0)
	p.sync(voice.TUIVoiceStatus{State: "idle"}, now.Add(overlayFrameInterval), 0)
	if len(fb.frames) != 1 {
		t.Fatalf("static idle state redrawn %d times, want 1", len(fb.frames))
	}

	// Animated states do redraw every frame.
	p.sync(voice.TUIVoiceStatus{State: "connecting"}, now.Add(2*overlayFrameInterval), 0)
	p.sync(voice.TUIVoiceStatus{State: "connecting"}, now.Add(3*overlayFrameInterval), 0)
	if len(fb.frames) != 3 {
		t.Fatalf("frames = %d, want 3 (idle once + two connecting frames)", len(fb.frames))
	}
}

func TestSyncErrorIsStatic(t *testing.T) {
	p, fb := newTestPlugin()
	now := time.Now()
	p.sync(voice.TUIVoiceStatus{State: "error"}, now, 0)
	p.sync(voice.TUIVoiceStatus{State: "error"}, now.Add(overlayFrameInterval), 0)
	if len(fb.frames) != 1 {
		t.Fatalf("error state redrawn %d times, want 1", len(fb.frames))
	}
}

func TestSyncLabelChangeRedraws(t *testing.T) {
	p, fb := newTestPlugin()
	now := time.Now()
	p.sync(voice.TUIVoiceStatus{State: "recording", PartialText: "hello"}, now, 0)
	now = now.Add(overlayFrameInterval)
	p.sync(voice.TUIVoiceStatus{State: "recording", PartialText: "hello world"}, now, 0)
	if len(fb.frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(fb.frames))
	}
	if fb.frames[1].label != "hello world" {
		t.Fatalf("label = %q, want the new partial text", fb.frames[1].label)
	}
}
