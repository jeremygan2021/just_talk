package voice

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
)

// levelRingSize is the number of recent peak-amplitude samples (one per
// streamAudio chunk, ~50ms apart at the default 6400-byte read buffer and
// 16kHz s16le mono) that Recorder exposes via Levels().
const levelRingSize = 64

// silencePeak is the normalized peak amplitude (0..1, i.e. peak/32768) below
// which an entire recording is treated as "no speech". A microphone keeps
// producing PCM even when nobody talks, so the byte count alone cannot tell a
// silent clip from an utterance; the session peak can.
//
// The value is deliberately conservative (about -46 dBFS), well under the
// loudness of even soft speech: wrongly skipping a quiet utterance would
// silently swallow the user's words, whereas letting borderline room noise
// through only costs one ASR round trip that every backend now terminates
// quickly with an empty result.
const silencePeak = 0.005

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

	// captured is the total number of PCM bytes delivered for the current
	// session: everything Read() handed to the ASR stream plus the tail
	// Stop() drained. Stop() itself only returns the bytes the streaming
	// reader had *not* consumed yet, which is normally zero because
	// streamAudio keeps the capture pipe drained while the user speaks.
	// "Did this session capture anything?" therefore has to come from
	// here, not from Stop()'s return value.
	captured atomic.Int64

	// peakNorm is the loudest normalized chunk peak seen during the session
	// (0..1). recordLevel and the tail accounting in Stop update it; the
	// voice plugin compares it against silencePeak to tell a silent
	// recording (bytes captured, nobody spoke) from real speech. Guarded by
	// levelRingMu.
	peakNorm float32
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
	r.captured.Store(0)
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
	if n > 0 {
		r.captured.Add(int64(n))
		if r.gain > 1 {
			applyGain(p[:n], r.gain)
		}
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
	if len(remaining) > 0 {
		// The tail was not delivered through Read(), so count it here to
		// keep CapturedBytes() a true session total, and fold its loudness
		// into the session peak so a short utterance that never made it
		// through streamAudio is not mistaken for silence.
		r.captured.Add(int64(len(remaining)))
		r.accountLoudness(remaining)
	}
	r.drainBuf.Reset()
	r.logger.Info("recording stopped", "remaining_bytes", len(remaining), "captured_bytes", r.captured.Load(), "session_peak", r.SessionPeak())
	return remaining, nil
}

// CapturedBytes returns the total number of PCM bytes captured during the
// current (or most recent) recording session, including audio that
// streamAudio already forwarded to the ASR backend. It is reset by Start().
//
// Callers must use this instead of the byte count returned by Stop() to
// decide whether a recording was silent: Stop() only returns the tail the
// streaming reader had not drained yet, so a normal utterance often stops
// with zero remaining bytes while still having captured plenty of audio.
func (r *Recorder) CapturedBytes() int64 { return r.captured.Load() }

// SessionPeak returns the loudest normalized amplitude (0..1) observed during
// the current (or most recent) session, or 0 when nothing was captured. It is
// reset by Start(). A microphone emits PCM even in a silent room, so callers
// use this to distinguish "captured audio, but nobody spoke" from real
// speech; see silencePeak.
func (r *Recorder) SessionPeak() float32 {
	r.levelRingMu.Lock()
	defer r.levelRingMu.Unlock()
	return r.peakNorm
}

// accountLoudness folds pcm's peak amplitude into the session peak without
// touching the level ring. Used by Stop for the tail that streamAudio never
// read.
func (r *Recorder) accountLoudness(pcm []byte) {
	norm := chunkPeak(pcm)
	r.levelRingMu.Lock()
	if norm > r.peakNorm {
		r.peakNorm = norm
	}
	r.levelRingMu.Unlock()
}

// chunkPeak returns the peak absolute amplitude of pcm (s16le mono)
// normalized to [0,1].
func chunkPeak(pcm []byte) float32 {
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
	return norm
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
	norm := chunkPeak(pcm)

	r.levelRingMu.Lock()
	r.levelRing[r.levelCount%levelRingSize] = norm
	r.levelCount++
	if norm > r.peakNorm {
		r.peakNorm = norm
	}
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
	r.peakNorm = 0
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
