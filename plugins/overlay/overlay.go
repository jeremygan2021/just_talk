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

// overlayFrameInterval is the overlay redraw period. 30 fps keeps the
// waveform animation smooth; static states are deduplicated and are
// never redrawn.
const overlayFrameInterval = 33 * time.Millisecond

// overlayFrame is one rendered frame of the status capsule.
type overlayFrame struct {
	// label is the short status text ("REC", "WAI", partial transcript).
	label string
	// accent is the color of the round status indicator.
	accent statusColor
	// bars holds the animated waveform bar heights in [0,1]. When empty
	// the backend renders the compact single-row capsule instead.
	bars []float32
	// phase is the animation clock in seconds, used for the breathing
	// glow and the indicator pulse.
	phase float64
}

type backend interface {
	Show(f overlayFrame) error
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
	animator    *waveformAnimator
	lastState   string
	lastLabel   string
	lastVisible bool
	lastWide    bool
}

func NewOverlayPlugin() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string    { return "overlay" }
func (p *Plugin) Version() string { return "0.1.0" }

func (p *Plugin) Init(env engine.PluginEnv) error {
	p.logger = env.Logger()
	p.cfg = env.Config().Overlay
	p.animator = newWaveformAnimator(barCount)
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

	ticker := time.NewTicker(overlayFrameInterval)
	defer ticker.Stop()
	started := time.Now()
	p.logger.Info("overlay started")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			now := time.Now()
			p.sync(voice.TUIStatus(), now, now.Sub(started).Seconds())
		}
	}
}

func (p *Plugin) Stop() error {
	if p.backend != nil {
		return p.backend.Close()
	}
	return nil
}

func (p *Plugin) sync(status voice.TUIVoiceStatus, now time.Time, phase float64) {
	label, color, levels, visible := displayForStatus(status, p.cfg.IdleVisible)

	// The waveform row is only shown while audio is (or was just) being
	// captured: during the recording itself and while the final
	// transcript is still being waited on. In the waiting states the
	// animator gets no fresh levels and simply eases the bars flat.
	wide := waveformState(status.State)
	var bars []float32
	if wide {
		bars = p.animator.Update(now, levels)
	} else {
		p.animator.Reset()
	}

	animating := animatedState(status.State)
	changed := status.State != p.lastState || label != p.lastLabel ||
		visible != p.lastVisible || wide != p.lastWide

	// Static states are only repainted when something actually changed;
	// animated states (recording, gesture confirmation, ...) redraw at
	// the frame rate so the glow and the bars keep moving.
	if !changed && !animating {
		return
	}
	p.lastState, p.lastLabel, p.lastVisible, p.lastWide = status.State, label, visible, wide

	if !visible {
		if err := p.backend.Hide(); err != nil {
			p.logger.Debug("overlay hide failed", "error", err)
		}
		return
	}
	frame := overlayFrame{label: label, accent: color, bars: bars, phase: phase}
	if err := p.backend.Show(frame); err != nil {
		p.logger.Debug("overlay show failed", "error", err)
	}
}

// waveformState reports whether a status renders the two-row capsule
// with the live waveform.
func waveformState(state string) bool {
	switch state {
	case "recording", "stopping", "stopping_delayed":
		return true
	default:
		return false
	}
}

// animatedState reports whether a status is worth redrawing at the frame
// rate. Idle and error are static: they are drawn once and left alone.
func animatedState(state string) bool {
	switch state {
	case "recording", "stopping", "stopping_delayed", "connecting", "enter", "undo":
		return true
	default:
		return false
	}
}

func displayForStatus(status voice.TUIVoiceStatus, idleVisible bool) (string, statusColor, []float32, bool) {
	switch status.State {
	case "connecting":
		return "CON", statusColor{R: 245 << 8, G: 190 << 8, B: 70 << 8}, nil, true
	case "recording":
		// Recording: show partial text when streaming ASR has
		// returned something, otherwise the "REC" abbreviation. The
		// waveform row is always present during recording.
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

// clipLabel returns the longest rune-prefix of label that fits in maxW
// pixels at the given bitmap scale. Multi-byte runes (for example
// Chinese characters) advance the cursor the same way drawSurfaceText
// does, so the clipped string never overlaps the waveform beside it.
func clipLabel(label string, scale, maxW int) string {
	if bitmapTextWidth(label, scale) <= maxW {
		return label
	}
	stride := 6 * scale
	if stride <= 0 {
		return ""
	}
	count := maxW / stride
	if count <= 0 {
		return ""
	}
	runes := []rune(label)
	if count >= len(runes) {
		return label
	}
	return string(runes[:count])
}
