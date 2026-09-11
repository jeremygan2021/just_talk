package overlay

import (
	"strings"
	"testing"

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

func TestSubsampleWaveform(t *testing.T) {
	// Input shorter than output: leading samples copied verbatim.
	got := subsampleWaveform([]float32{0.5, 0.6, 0.7, 0.8})
	if len(got) != waveformBars {
		t.Fatalf("subsampleWaveform len = %d, want %d", len(got), waveformBars)
	}
	if got[0] != 0.5 || got[1] != 0.6 || got[2] != 0.7 || got[3] != 0.8 {
		t.Fatalf("leading samples not copied: %v", got[:4])
	}
	for i := 4; i < waveformBars; i++ {
		if got[i] != 0 {
			t.Fatalf("subsampleWaveform[%d] = %f, want 0", i, got[i])
		}
	}

	// Input larger than output: averaged windows.
	got = subsampleWaveform([]float32{0.0, 1.0, 0.0, 1.0, 0.0, 1.0})
	// step = 6/24 = 0.25, each bar covers a 0.25-index window. For 6
	// samples, the first 4 bars take samples 0..3 alternating, then 2..5.
	if got[0] != 0.0 || got[1] != 1.0 || got[2] != 0.0 || got[3] != 1.0 {
		t.Fatalf("averaged waveform: %v", got[:6])
	}
}

func TestSubsampleWaveformEmpty(t *testing.T) {
	got := subsampleWaveform(nil)
	if len(got) != waveformBars {
		t.Fatalf("subsampleWaveform(nil) len = %d, want %d", len(got), waveformBars)
	}
	for i, v := range got {
		if v != 0 {
			t.Fatalf("subsampleWaveform(nil)[%d] = %f, want 0", i, v)
		}
	}
}

func TestLevelsEqual(t *testing.T) {
	if !levelsEqual(nil, nil) {
		t.Fatal("nil == nil should be true")
	}
	if levelsEqual(nil, []float32{0}) {
		t.Fatal("nil vs non-empty should be false")
	}
	if !levelsEqual([]float32{0.1, 0.2}, []float32{0.1, 0.2}) {
		t.Fatal("same content should be equal")
	}
	if levelsEqual([]float32{0.1, 0.2}, []float32{0.1, 0.3}) {
		t.Fatal("different values should not be equal")
	}
}
