package voice

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
)

// levelRingSize is the number of recent peak-amplitude samples (one per
// streamAudio chunk, ~50ms apart at the default 6400-byte read buffer and
// 16kHz s16le mono) that Recorder exposes via Levels().
const levelRingSize = 64

// Recorder captures audio from the default microphone as PCM 16kHz 16bit mono.
type Recorder struct {
	logger   *slog.Logger
	device   string
	gain     int
	mu       sync.Mutex
	readMu   sync.Mutex
	stdout   io.ReadCloser
	stopFunc func() error
	drainBuf bytes.Buffer
	started  bool
	backend  string

	// levelRing is a fixed-size ring of normalized peak amplitudes
	// (0..1, linear, not dB). It is appended to by streamAudio and read
	// by Levels(). levelRingMu guards both the slice and the count.
	levelRing   [levelRingSize]float32
	levelCount  int
	levelRingMu sync.Mutex
}

func NewRecorder(logger *slog.Logger, gain int) *Recorder {
	if gain < 1 {
		gain = 1
	}
	return &Recorder{logger: logger, gain: gain}
}

func NewRecorderWithDevice(logger *slog.Logger, device string, gain int) *Recorder {
	if gain < 1 {
		gain = 1
	}
	return &Recorder{logger: logger, device: device, gain: gain}
}

func (r *Recorder) Backend() string { return r.backend }

func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return nil
	}

	stdout, name, stopFunc, err := startCaptureWithDevice(r.logger, r.device)
	if err != nil {
		return err
	}
	r.stdout = stdout
	r.stopFunc = stopFunc
	r.started = true
	r.backend = name
	r.resetLevels()
	r.logger.Info("recording started", "backend", name)
	return nil
}

func (r *Recorder) Read(p []byte) (int, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()

	r.mu.Lock()
	if r.drainBuf.Len() > 0 {
		n, _ := r.drainBuf.Read(p)
		r.mu.Unlock()
		return n, nil
	}
	stdout := r.stdout
	r.mu.Unlock()

	if stdout == nil {
		return 0, io.EOF
	}

	n, err := stdout.Read(p)
	if n > 0 && r.gain > 1 {
		applyGain(p[:n], r.gain)
	}
	if errors.Is(err, os.ErrClosed) {
		err = io.EOF
	}
	return n, err
}

func (r *Recorder) Stop() ([]byte, error) {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil, nil
	}
	r.started = false
	stopFunc := r.stopFunc
	stdout := r.stdout
	r.stopFunc = nil
	r.stdout = nil
	r.mu.Unlock()

	if stopFunc != nil {
		_ = stopFunc()
	}

	r.readMu.Lock()
	defer r.readMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	if stdout != nil {
		buf := make([]byte, 640)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				applyGain(buf[:n], r.gain)
				r.drainBuf.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}

	remaining := r.drainBuf.Bytes()
	r.drainBuf.Reset()
	r.logger.Info("recording stopped", "remaining_bytes", len(remaining))
	return remaining, nil
}

// applyGain multiplies s16le samples by gain factor, clamping to valid range.
func applyGain(pcm []byte, gain int) {
	for i := 0; i+1 < len(pcm); i += 2 {
		s := int16(pcm[i]) | int16(pcm[i+1])<<8
		s32 := int32(s) * int32(gain)
		if s32 > 32767 {
			s32 = 32767
		} else if s32 < -32768 {
			s32 = -32768
		}
		pcm[i] = byte(s32)
		pcm[i+1] = byte(s32 >> 8)
	}
}

// recordLevel appends the peak absolute amplitude of pcm (s16le mono) to
// the recorder's level ring as a normalized value in [0,1]. The caller
// may pass an empty slice to push a silent sample (useful for end-of-stream
// padding). Safe to call concurrently with Levels().
func (r *Recorder) recordLevel(pcm []byte) {
	var peak int32
	for i := 0; i+1 < len(pcm); i += 2 {
		s := int32(int16(pcm[i]) | int16(pcm[i+1])<<8)
		if s < 0 {
			s = -s
		}
		if s > peak {
			peak = s
		}
	}
	norm := float32(peak) / 32768.0
	if norm < 0 {
		norm = 0
	} else if norm > 1 {
		norm = 1
	}

	r.levelRingMu.Lock()
	r.levelRing[r.levelCount%levelRingSize] = norm
	r.levelCount++
	r.levelRingMu.Unlock()
}

// Levels returns a copy of the most recent peak-amplitude samples in
// chronological order (oldest first, newest last). Samples that have not
// been filled yet are zero. The returned slice is independent of the
// recorder's internal state and may be retained by the caller.
func (r *Recorder) Levels() []float32 {
	r.levelRingMu.Lock()
	count := r.levelCount
	out := make([]float32, levelRingSize)
	if count == 0 {
		r.levelRingMu.Unlock()
		return out
	}
	if count < levelRingSize {
		// Ring not yet full: only the first `count` entries are valid.
		copy(out[:count], r.levelRing[:count])
		r.levelRingMu.Unlock()
		return out[:count]
	}
	// Ring is full and has wrapped. The next slot to write is
	// count % levelRingSize, so the oldest sample lives there.
	start := count % levelRingSize
	copy(out, r.levelRing[start:])
	copy(out[levelRingSize-start:], r.levelRing[:start])
	r.levelRingMu.Unlock()
	return out
}

// resetLevels clears the level ring. Should be called when a new
// recording session starts so leftover amplitudes from a previous
// session don't bleed into the new one.
func (r *Recorder) resetLevels() {
	r.levelRingMu.Lock()
	r.levelCount = 0
	for i := range r.levelRing {
		r.levelRing[i] = 0
	}
	r.levelRingMu.Unlock()
}

// LevelCount returns the number of valid amplitude samples currently
// held in the ring (0 when no recording has happened yet). Useful for
// distinguishing "no data yet" from "silence".
func (r *Recorder) LevelCount() int {
	r.levelRingMu.Lock()
	defer r.levelRingMu.Unlock()
	return r.levelCount
}
