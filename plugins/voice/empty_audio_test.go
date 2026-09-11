package voice

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// TestOfflineASREmptyAudioFastPath is the regression test for the
// "silent recording leaves the WAI overlay stuck" bug. Before the fix the
// empty-buffer branch returned nil without closing Final or Done, so the
// voice plugin waited the full 15s ASR timeout, then re-dispatched the
// previous session's transcript.
func TestOfflineASREmptyAudioFastPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := NewOfflineASRClient(OfflineASRConfig{}, logger)

	// Seed lastText the way a previous successful session would leave it.
	c.mu.Lock()
	c.lastText = "stale transcript"
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.SendAudio(ctx, nil, true); err != nil {
		t.Fatalf("SendAudio(final, empty): %v", err)
	}

	select {
	case <-c.Final():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Final() did not close within 500ms on empty audio")
	}

	if got := c.LastText(); got != "" {
		t.Fatalf("LastText() = %q, want empty after silent recording", got)
	}

	select {
	case r := <-c.Results():
		if !r.IsFinal || r.Text != "" {
			t.Fatalf("result = %+v, want empty final", r)
		}
	default:
		t.Fatal("expected one empty final result on Results()")
	}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestURLASREmptyAudioFastPath covers the same regression for the Doubao
// file-URL backend, and also verifies that Close() closes resultCh so the
// consumer goroutine launched by voice.connectASR can exit cleanly.
func TestURLASREmptyAudioFastPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := NewURLASRClient(URLASRConfig{APIKey: "test"}, logger)

	c.mu.Lock()
	c.lastText = "stale transcript"
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.SendAudio(ctx, nil, true); err != nil {
		t.Fatalf("SendAudio(final, empty): %v", err)
	}

	select {
	case <-c.Final():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Final() did not close within 500ms on empty audio")
	}

	if got := c.LastText(); got != "" {
		t.Fatalf("LastText() = %q, want empty after silent recording", got)
	}

	consumed := make(chan struct{})
	go func() {
		for range c.Results() {
		}
		close(consumed)
	}()

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-consumed:
	case <-time.After(time.Second):
		t.Fatal("consumer goroutine did not exit after Close()")
	}
}

// TestASRClientEmptyFinalClosesAndResets covers the regression for the
// default Doubao streaming backend: when the server returns an empty final
// transcript (which happens for silent clips), Final must close so the
// voice plugin's 15s timeout does not fire, and lastText must be cleared so
// the previous session's text is not re-dispatched.
func TestASRClientEmptyFinalClosesAndResets(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := NewASRClient(ASRConfig{}, logger)

	// Seed lastText as if a previous session had succeeded.
	c.textMu.Lock()
	c.lastText = "stale transcript"
	c.textMu.Unlock()

	// A msgType=0x09 server response with flag=0x02 (final) and payload
	// {"result":{"text":""}}. Header layout:
	// [version|size, type<<4|flags, codec, ?, payloadSize(4 bytes BE), payload]
	msg := buildASRResponseFrame(0x02, `{"result":{"text":""}}`)

	c.parseResponse(msg)

	select {
	case <-c.Final():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Final() did not close within 500ms on empty final response")
	}
	if got := c.LastText(); got != "" {
		t.Fatalf("LastText() = %q, want empty after empty final", got)
	}
}

// TestASRClientNonEmptyFinalKeepsText guards the streaming path's happy
// case so the empty-final fix does not regress the normal flow.
func TestASRClientNonEmptyFinalKeepsText(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := NewASRClient(ASRConfig{}, logger)

	msg := buildASRResponseFrame(0x02, `{"result":{"text":"hello"}}`)

	c.parseResponse(msg)

	select {
	case <-c.Final():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Final() did not close within 500ms")
	}
	if got := c.LastText(); got != "hello" {
		t.Fatalf("LastText() = %q, want hello", got)
	}
}

// buildASRResponseFrame synthesises a Doubao streaming ASR server-response
// frame. Wire layout is: 4-byte protocol header, 4-byte sequence id,
// 4-byte big-endian payload size, JSON payload. flags belongs to the
// lower nibble of byte 1 (0x02 for final, 0x03 for final+last).
func buildASRResponseFrame(flags byte, payload string) []byte {
	header := []byte{hdrVersion | hdrHeaderSize, byte(0x09<<4) | (flags & 0x0F), hdrRawAudio, 0x00}
	seq := []byte{0, 0, 0, 0}
	size := make([]byte, 4)
	pb := []byte(payload)
	size[0] = byte(len(pb) >> 24)
	size[1] = byte(len(pb) >> 16)
	size[2] = byte(len(pb) >> 8)
	size[3] = byte(len(pb))
	out := append(header, seq...)
	out = append(out, size...)
	out = append(out, pb...)
	return out
}

// fakeASRBackend records the calls finishRecordingSession makes on the ASR
// backend. It returns an empty transcript and an immediately-closed Final
// channel so the voice plugin's empty-audio path can run end-to-end.
type fakeASRBackend struct {
	sendCalls   int
	lastPayload []byte
	lastIsLast  bool
	closeCalls  int
	final       chan struct{}
	done        chan struct{}
	closed      bool
	finalText   string
	mu          sync.Mutex
}

func newFakeASRBackend() *fakeASRBackend {
	return &fakeASRBackend{
		final: make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// setFinalResult queues a transcript to be returned by LastText() and
// closes Final so the voice plugin's "ASR final received" branch fires.
// Pass an empty string to exercise the non-dispatch path without touching
// the real clipboard or auto-submit.
func (b *fakeASRBackend) setFinalResult(text string) {
	b.mu.Lock()
	b.finalText = text
	close(b.final)
	b.mu.Unlock()
}

func (b *fakeASRBackend) Connect(ctx context.Context) error { return nil }

func (b *fakeASRBackend) SendAudio(ctx context.Context, pcm []byte, isLast bool) error {
	b.mu.Lock()
	b.sendCalls++
	b.lastPayload = append([]byte(nil), pcm...)
	b.lastIsLast = isLast
	b.mu.Unlock()
	return nil
}

func (b *fakeASRBackend) Results() <-chan ASRResult { return make(chan ASRResult) }

func (b *fakeASRBackend) StartReceive(ctx context.Context) {}

func (b *fakeASRBackend) Done() <-chan struct{}  { return b.done }
func (b *fakeASRBackend) Final() <-chan struct{} { return b.final }

func (b *fakeASRBackend) LastText() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.finalText
}

func (b *fakeASRBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	b.closeCalls++
	close(b.done)
	return nil
}

// TestFinishRecordingSessionEmptyAudioSkipsASR is the voice-plugin-level
// regression test for the same bug. Even though the offline and URL
// backends already publish an empty final result on empty input, the
// streaming backend relies on a server response that the remote service
// does not always send for silent clips. Skipping the ASR round-trip
// entirely when the recorder captured no bytes keeps the overlay from
// waiting on a network response that may never come, and keeps the
// connection healthy for the next session.
func TestFinishRecordingSessionEmptyAudioSkipsASR(t *testing.T) {
	p := testVoicePlugin()
	backend := newFakeASRBackend()

	rec := NewRecorder(p.logger, 1)
	// A recorder that was never started has a zero session byte count and
	// Stop() returns nothing, so finishRecordingSession's emptyAudio check
	// (captured bytes == 0) takes the silent fast path.

	p.mu.Lock()
	p.sessionID = 1
	p.userStopped = true
	p.pendingDone = 1
	p.finishingSessions = map[uint64]struct{}{1: {}}
	p.mu.Unlock()

	session := &recordingSession{
		sessionID:   1,
		recorder:    rec,
		asrClient:   backend,
		autoSubmit:  true,
		userStopped: true,
	}

	start := time.Now()
	p.finishRecordingSession(session)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("finishRecordingSession took %v, want <500ms for empty audio", elapsed)
	}

	backend.mu.Lock()
	sendCalls := backend.sendCalls
	closeCalls := backend.closeCalls
	backend.mu.Unlock()
	if sendCalls != 0 {
		t.Fatalf("SendAudio called %d times on empty audio, want 0", sendCalls)
	}
	if closeCalls != 1 {
		t.Fatalf("Close called %d times, want 1", closeCalls)
	}

	p.mu.Lock()
	pending := p.pendingDone
	p.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pendingDone = %d, want 0 after empty-audio finish", pending)
	}
}

// newCaptureRecorder builds a Recorder backed by an in-memory PCM buffer
// instead of a real arecord process. It lets tests replay a whole session -
// including the "streamAudio already drained the pipe before Stop()" shape
// that used to be mistaken for silence - without touching the microphone.
func newCaptureRecorder(pcm []byte) *Recorder {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewRecorder(logger, 1)
	r.mu.Lock()
	r.stdout = io.NopCloser(bytes.NewReader(pcm))
	r.stopFunc = func() error { return nil }
	r.started = true
	r.mu.Unlock()
	return r
}

// drainRecorder mimics voice.streamAudio: it reads the capture source to EOF
// so the bytes land in the recorder's session total instead of staying in the
// buffer that Stop() returns.
func drainRecorder(r *Recorder) {
	buf := make([]byte, 6400)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			r.recordLevel(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// TestRecorderCapturedBytesSurvivesStreamDrain is the recorder-level
// regression test for the "ASR never runs" bug. Once streamAudio has read the
// capture pipe, Stop() returns zero bytes because nothing was left to drain,
// even though the session captured plenty of audio. CapturedBytes must still
// report the real total so the voice plugin does not treat the recording as
// silent.
func TestRecorderCapturedBytesSurvivesStreamDrain(t *testing.T) {
	pcm := make([]byte, 12800) // ~400ms at 16kHz s16le mono
	for i := range pcm {
		pcm[i] = byte(i)
	}
	rec := newCaptureRecorder(pcm)

	drainRecorder(rec)
	if got := rec.CapturedBytes(); got != int64(len(pcm)) {
		t.Fatalf("CapturedBytes after stream drain = %d, want %d", got, len(pcm))
	}

	remaining, err := rec.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining = %d bytes, want 0 after the stream drained the pipe", len(remaining))
	}
	if got := rec.CapturedBytes(); got != int64(len(pcm)) {
		t.Fatalf("CapturedBytes after Stop = %d, want %d (must not double count)", got, len(pcm))
	}
}

// TestRecorderCapturedBytesCountsUndrainedTail covers the other side of the
// same contract: when nothing had been streamed yet (for example the hotkey
// was released before the ASR connection came up), all the audio is still
// counted once Stop() drains it.
func TestRecorderCapturedBytesCountsUndrainedTail(t *testing.T) {
	pcm := make([]byte, 4096)
	rec := newCaptureRecorder(pcm)

	remaining, err := rec.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(remaining) != len(pcm) {
		t.Fatalf("remaining = %d bytes, want %d", len(remaining), len(pcm))
	}
	if got := rec.CapturedBytes(); got != int64(len(pcm)) {
		t.Fatalf("CapturedBytes = %d, want %d", got, len(pcm))
	}
}

// loudPCM returns n bytes of s16le PCM at a constant amplitude that is well
// above silencePeak, i.e. something the silence gate treats as speech.
func loudPCM(n int) []byte {
	pcm := make([]byte, n)
	for i := 0; i+1 < n; i += 2 {
		pcm[i] = 0x40 // 0x4000 = 16384, normalized peak 0.5
		pcm[i+1] = 0x40
	}
	return pcm
}

// TestFinishRecordingSessionStreamedAudioStillSendsFinal is the
// voice-plugin-level regression test for the bug where every normal
// recording was treated as silent. streamAudio drains the capture pipe while
// the user speaks, so Stop() returns no remaining bytes; the session must
// still send the final audio marker and wait for the ASR final, otherwise
// partial text is shown in the overlay but no transcript is ever pasted.
func TestFinishRecordingSessionStreamedAudioStillSendsFinal(t *testing.T) {
	p := testVoicePlugin()
	backend := newFakeASRBackend()
	// An empty transcript keeps the test off the real clipboard/auto-submit
	// path while still asserting that the ASR round-trip happens at all.
	backend.setFinalResult("")

	rec := newCaptureRecorder(loudPCM(9600))
	drainRecorder(rec)
	if rec.CapturedBytes() == 0 {
		t.Fatal("test setup: recorder captured no bytes")
	}
	if rec.SessionPeak() < silencePeak {
		t.Fatalf("test setup: session peak %v is below silencePeak %v", rec.SessionPeak(), silencePeak)
	}

	p.mu.Lock()
	p.sessionID = 7
	p.userStopped = true
	p.pendingDone = 1
	p.finishingSessions = map[uint64]struct{}{7: {}}
	p.mu.Unlock()

	session := &recordingSession{
		sessionID:   7,
		recorder:    rec,
		asrClient:   backend,
		userStopped: true,
	}

	start := time.Now()
	p.finishRecordingSession(session)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("finishRecordingSession took %v; Final() should have fired immediately", elapsed)
	}

	backend.mu.Lock()
	sendCalls, isLast := backend.sendCalls, backend.lastIsLast
	backend.mu.Unlock()
	if sendCalls != 1 {
		t.Fatalf("SendAudio called %d times for a non-empty recording, want 1", sendCalls)
	}
	if !isLast {
		t.Fatal("the final SendAudio call must carry isLast=true so the backend flushes the utterance")
	}

	p.mu.Lock()
	pending := p.pendingDone
	p.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pendingDone = %d, want 0 after the finish completed", pending)
	}
}

// TestRecorderSessionPeakTracksLoudness verifies the session peak tracks the
// loudest chunk, including chunks that only reach the stream reader.
func TestRecorderSessionPeakTracksLoudness(t *testing.T) {
	rec := newCaptureRecorder(s16le([]int16{100, -200, 300}))
	drainRecorder(rec)
	want := float32(300) / 32768.0
	if got := rec.SessionPeak(); got != want {
		t.Fatalf("SessionPeak = %v, want %v", got, want)
	}
}

// TestRecorderSessionPeakIncludesUndrainedTail verifies that a short utterance
// which only ends up in the Stop() drain still counts as voiced, so the
// silence gate cannot swallow it.
func TestRecorderSessionPeakIncludesUndrainedTail(t *testing.T) {
	rec := newCaptureRecorder(loudPCM(3200))

	remaining, err := rec.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(remaining) == 0 {
		t.Fatal("test setup: expected the tail to be drained by Stop")
	}
	if got := rec.SessionPeak(); got < silencePeak {
		t.Fatalf("SessionPeak = %v after draining a loud tail, want >= %v", got, silencePeak)
	}
}

// TestFinishRecordingSessionSilentAudioSkipsASR is the regression test for
// "pressing the hotkey without speaking parks the overlay on WAI and then
// flashes ERR". A live microphone still emits PCM in a quiet room, so the
// recording has bytes but no speech; the plugin must skip the ASR round-trip
// and return to idle immediately instead of waiting for a final that the
// backend will never produce.
func TestFinishRecordingSessionSilentAudioSkipsASR(t *testing.T) {
	p := testVoicePlugin()
	backend := newFakeASRBackend()
	// Deliberately do NOT close Final: if the silence gate fails, the finish
	// would block for asrFinalTimeout, which the elapsed check below catches.

	// Amplitude 40 / 32768 ~= 0.0012, well below silencePeak: microphone
	// floor noise, not speech.
	quiet := s16le([]int16{40, -40, 30, -30, 20, -20})
	rec := newCaptureRecorder(quiet)
	drainRecorder(rec)
	if rec.CapturedBytes() == 0 {
		t.Fatal("test setup: silent recording should still have captured bytes")
	}
	if rec.SessionPeak() >= silencePeak {
		t.Fatalf("test setup: session peak %v should be below silencePeak %v", rec.SessionPeak(), silencePeak)
	}

	p.asrFinalTimeout = 2 * time.Second // safety net if the gate regresses

	p.mu.Lock()
	p.sessionID = 11
	p.userStopped = true
	p.pendingDone = 1
	p.finishingSessions = map[uint64]struct{}{11: {}}
	p.mu.Unlock()

	session := &recordingSession{
		sessionID:   11,
		recorder:    rec,
		asrClient:   backend,
		userStopped: true,
	}

	start := time.Now()
	p.finishRecordingSession(session)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("finishRecordingSession took %v, want the silent fast path", elapsed)
	}

	backend.mu.Lock()
	sendCalls, closeCalls := backend.sendCalls, backend.closeCalls
	backend.mu.Unlock()
	if sendCalls != 0 {
		t.Fatalf("SendAudio called %d times for silent audio, want 0", sendCalls)
	}
	if closeCalls != 1 {
		t.Fatalf("Close called %d times, want 1", closeCalls)
	}

	p.mu.Lock()
	lastErr, pending := p.lastError, p.pendingDone
	p.mu.Unlock()
	if lastErr != "" {
		t.Fatalf("silent recording must not set an error status, got %q", lastErr)
	}
	if pending != 0 {
		t.Fatalf("pendingDone = %d, want 0 after the silent-audio finish", pending)
	}
}

// TestFinishRecordingSessionFinalTimeoutWithoutPartialIsNotAnError covers the
// streaming backend's no-speech case that slips past the local silence gate
// (for example a noisy room): if the backend never returns a final and never
// produced a partial either, the plugin must go idle without flashing ERR.
func TestFinishRecordingSessionFinalTimeoutWithoutPartialIsNotAnError(t *testing.T) {
	p := testVoicePlugin()
	backend := newFakeASRBackend()
	// Final is never closed and LastText stays empty.

	rec := newCaptureRecorder(loudPCM(9600))
	drainRecorder(rec)

	p.asrFinalTimeout = 50 * time.Millisecond

	p.mu.Lock()
	p.sessionID = 12
	p.userStopped = true
	p.pendingDone = 1
	p.finishingSessions = map[uint64]struct{}{12: {}}
	p.mu.Unlock()

	session := &recordingSession{
		sessionID:   12,
		recorder:    rec,
		asrClient:   backend,
		userStopped: true,
	}

	p.finishRecordingSession(session)

	backend.mu.Lock()
	sendCalls := backend.sendCalls
	backend.mu.Unlock()
	if sendCalls != 1 {
		t.Fatalf("SendAudio called %d times, want 1 (audio was loud enough to submit)", sendCalls)
	}

	p.mu.Lock()
	lastErr := p.lastError
	p.mu.Unlock()
	if lastErr != "" {
		t.Fatalf("a no-partial timeout must not set ERR, got %q", lastErr)
	}
}

// TestFinishRecordingSessionFinalTimeoutWithPartialIsAnError is the other
// half of the contract: when partial hypotheses arrived but the final never
// did, that is a genuine backend failure and must still surface ERR.
func TestFinishRecordingSessionFinalTimeoutWithPartialIsAnError(t *testing.T) {
	p := testVoicePlugin()
	backend := newFakeASRBackend()
	backend.mu.Lock()
	backend.finalText = "partial words"
	backend.mu.Unlock()

	rec := newCaptureRecorder(loudPCM(9600))
	drainRecorder(rec)

	p.asrFinalTimeout = 50 * time.Millisecond

	p.mu.Lock()
	p.sessionID = 13
	p.userStopped = true
	p.pendingDone = 1
	p.finishingSessions = map[uint64]struct{}{13: {}}
	p.mu.Unlock()

	session := &recordingSession{
		sessionID: 13,
		recorder:  rec,
		asrClient: backend,
		// userStopped stays false so the partial text is not actually
		// dispatched to the clipboard; this test only asserts the ERR status.
		userStopped: false,
	}

	p.finishRecordingSession(session)

	p.mu.Lock()
	lastErr := p.lastError
	p.mu.Unlock()
	if lastErr == "" {
		t.Fatal("a timeout after partials must surface an ERR status")
	}
}

// TestFinishRecordingSessionNoSpeechGraceEndsEarly verifies the streaming
// backend's silent case does not hold WAI for the whole final timeout: with no
// partial hypothesis at all, the finish must give up after the shorter
// no-speech grace and go idle without ERR.
func TestFinishRecordingSessionNoSpeechGraceEndsEarly(t *testing.T) {
	p := testVoicePlugin()
	backend := newFakeASRBackend() // Final never closes, LastText stays empty

	rec := newCaptureRecorder(loudPCM(9600))
	drainRecorder(rec)

	p.asrFinalTimeout = 5 * time.Second
	p.asrNoSpeechTimeout = 60 * time.Millisecond

	p.mu.Lock()
	p.sessionID = 14
	p.userStopped = true
	p.pendingDone = 1
	p.finishingSessions = map[uint64]struct{}{14: {}}
	p.mu.Unlock()

	session := &recordingSession{
		sessionID: 14,
		recorder:  rec,
		asrClient: backend,
	}

	start := time.Now()
	p.finishRecordingSession(session)
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("finishRecordingSession took %v; the no-speech grace should have ended it", elapsed)
	}

	p.mu.Lock()
	lastErr := p.lastError
	p.mu.Unlock()
	if lastErr != "" {
		t.Fatalf("the no-speech grace must not set ERR, got %q", lastErr)
	}
}
