package overlay

import (
	"context"
	"log/slog"
	"time"

	"github.com/c/just-talk-go/config"
	"github.com/c/just-talk-go/engine"
	"github.com/c/just-talk-go/plugins/voice"
)

// partialTextMax is the maximum number of runes of ASR partial text
// that we forward to the overlay. Beyond that the overlay capsule would
// eat most of the screen. The text is taken from the end of the string
// (latest spoken portion) so the user always sees the freshest words.
const partialTextMax = 30

// waveformBars is the number of bars rendered in the recording waveform.
// The recorder keeps 64 amplitude samples; we keep all 64 but the bar
// rendering averages adjacent pairs to fit `waveformBars` columns.
const waveformBars = 24

type backend interface {
	// Show renders the overlay. `label` is the short status text
	// ("REC", "WAI", partial transcript, etc.). `color` is the dot
	// color. `levels` is a slice of normalized peak amplitudes
	// (0..1, oldest first, newest last). When non-empty the backend
	// is expected to switch to a wider layout that includes a
	// waveform visualization.
	Show(label string, color statusColor, levels []float32) error
	Hide() error
	Close() error
}

type statusColor struct {
	R uint16
	G uint16
	B uint16
}

type Plugin struct {
	logger      *slog.Logger
	cfg         config.OverlayConfig
	backend     backend
	lastState   string
	lastLabel   string
	lastVisible bool
	lastLevels  []float32
}

func NewOverlayPlugin() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string    { return "overlay" }
func (p *Plugin) Version() string { return "0.1.0" }

func (p *Plugin) Init(env engine.PluginEnv) error {
	p.logger = env.Logger()
	p.cfg = env.Config().Overlay
	return nil
}

func (p *Plugin) Start(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	b, err := newBackend(p.cfg)
	if err != nil {
		p.logger.Warn("overlay unavailable", "error", err)
		return nil
	}
	p.backend = b
	defer p.backend.Close()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	p.logger.Info("overlay started")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			p.sync(voice.TUIStatus())
		}
	}
}

func (p *Plugin) Stop() error {
	if p.backend != nil {
		return p.backend.Close()
	}
	return nil
}

func (p *Plugin) sync(status voice.TUIVoiceStatus) {
	label, color, levels, visible := displayForStatus(status, p.cfg.IdleVisible)
	if status.State == p.lastState && label == p.lastLabel && visible == p.lastVisible && levelsEqual(p.lastLevels, levels) {
		return
	}
	p.lastState, p.lastLabel, p.lastVisible = status.State, label, visible
	p.lastLevels = levels

	if !visible {
		if err := p.backend.Hide(); err != nil {
			p.logger.Debug("overlay hide failed", "error", err)
		}
		return
	}
	if err := p.backend.Show(label, color, levels); err != nil {
		p.logger.Debug("overlay show failed", "error", err)
	}
}

func levelsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func displayForStatus(status voice.TUIVoiceStatus, idleVisible bool) (string, statusColor, []float32, bool) {
	switch status.State {
	case "connecting":
		return "CON", statusColor{R: 245 << 8, G: 190 << 8, B: 70 << 8}, nil, true
	case "recording":
		// Recording: show partial text when streaming ASR has
		// returned something, otherwise the "REC" abbreviation. The
		// waveform bars are always present during recording.
		label := "REC"
		if t := trimPartial(status.PartialText); t != "" {
			label = t
		}
		return label, statusColor{R: 255 << 8, G: 65 << 8, B: 65 << 8}, status.LevelSamples, true
	case "stopping_delayed":
		return "STP", statusColor{R: 255 << 8, G: 140 << 8, B: 60 << 8}, nil, true
	case "stopping":
		return "WAI", statusColor{R: 255 << 8, G: 160 << 8, B: 70 << 8}, nil, true
	case "enter":
		// Triple-tap gesture: bent Return arrow instead of the record dot.
		return "⏎", statusColor{R: 80 << 8, G: 210 << 8, B: 120 << 8}, nil, true
	case "undo":
		// Double-tap gesture: curved retract arrow instead of the record dot.
		return "↶", statusColor{R: 255 << 8, G: 170 << 8, B: 60 << 8}, nil, true
	case "error":
		return "ERR", statusColor{R: 255 << 8, G: 65 << 8, B: 65 << 8}, nil, true
	default:
		return "IDL", statusColor{R: 145 << 8, G: 145 << 8, B: 145 << 8}, nil, idleVisible
	}
}

// trimPartial returns the last N runes of text, suitable for overlay
// display. It returns "" when text is empty or only whitespace.
func trimPartial(text string) string {
	if text == "" {
		return ""
	}
	runes := []rune(text)
	// Drop trailing whitespace so the visible label doesn't end with
	// half-rendered punctuation when the user is mid-sentence.
	for len(runes) > 0 && (runes[len(runes)-1] == ' ' || runes[len(runes)-1] == '\n') {
		runes = runes[:len(runes)-1]
	}
	if len(runes) == 0 {
		return ""
	}
	if len(runes) > partialTextMax {
		runes = runes[len(runes)-partialTextMax:]
	}
	return string(runes)
}

// truncateLabelWidth finds the longest rune-prefix of label whose
// bitmap width at the given scale fits inside maxW, and returns the
// width of that prefix. If label itself fits, the full width is
// returned. Multi-byte runes (for example Chinese characters) advance
// the same way drawText advances its cursor: each rune consumes
// 6*scale pixels.
//
// Used by the rendering backends to shrink a long ASR partial
// transcript down so it fits alongside the dot and the waveform bars
// in the recording-state capsule.
func truncateLabelWidth(label string, scale, maxW int) int {
	if bitmapTextWidth(label, scale) <= maxW {
		return bitmapTextWidth(label, scale)
	}
	stride := 6 * scale
	if stride <= 0 {
		return 0
	}
	count := maxW / stride
	if count <= 0 {
		return 0
	}
	runes := []rune(label)
	if count >= len(runes) {
		count = len(runes)
	}
	return count * stride
}

// subsampleWaveform reduces a sequence of amplitude samples to
// waveformBars columns by averaging adjacent windows. The returned
// slice always has exactly waveformBars entries. Callers may rely on
// this for sizing the waveform rendering area.
//
// When the recorder has fewer samples than waveformBars (very short
// recordings or first few hundred ms of a session) the leading samples
// are copied verbatim and the remaining bars are silent. When the
// recorder has more samples than waveformBars, each output bar
// represents the average of an evenly-sized input window.
func subsampleWaveform(samples []float32) []float32 {
	out := make([]float32, waveformBars)
	if len(samples) == 0 {
		return out
	}
	if len(samples) <= waveformBars {
		copy(out, samples)
		return out
	}
	step := float32(len(samples)) / float32(waveformBars)
	for i := 0; i < waveformBars; i++ {
		start := int(float32(i) * step)
		end := int(float32(i+1) * step)
		if end <= start {
			end = start + 1
		}
		if end > len(samples) {
			end = len(samples)
		}
		var sum float32
		var count int
		for j := start; j < end; j++ {
			sum += samples[j]
			count++
		}
		out[i] = sum / float32(count)
	}
	return out
}
