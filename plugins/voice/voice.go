package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/c/just-talk-go/config"
	"github.com/c/just-talk-go/engine"
	"github.com/c/just-talk-go/hotkey"
	"github.com/c/just-talk-go/internal/autotype"
	"github.com/c/just-talk-go/internal/clipboard"
)

const (
	defaultStopDelayMs = 800
	defaultMultiTapMs  = 500
	errorHoldDuration  = 10 * time.Second
	// gestureHintDuration is how long the overlay keeps showing the Enter or
	// retract hint after a multi-tap gesture has fired.
	gestureHintDuration = 1200 * time.Millisecond
	// undoTapActionUndo / undoTapActionClear select the retract payload.
	undoTapActionUndo  = "undo"
	undoTapActionClear = "clear"
	// defaultASRFinalTimeout bounds how long finishRecordingSession waits for
	// the backend's final transcript after the last audio packet.
	defaultASRFinalTimeout = 15 * time.Second
	// defaultASRNoSpeechTimeout is how long the final wait tolerates a
	// backend that has produced no partial hypothesis at all. A server that
	// heard speech normally streams a partial within a second; no partial at
	// all almost always means the clip was silence (or back-end noise), so we
	// stop waiting early and return to idle instead of holding WAI for the
	// full final timeout.
	defaultASRNoSpeechTimeout = 4 * time.Second
)

var (
	cancelRecordingCombo = hotkey.Combo{Mods: hotkey.ModNone, Key: hotkey.KeyEscape}
	retryErrorCombo      = hotkey.Combo{Mods: hotkey.ModNone, Key: hotkey.KeyR}
)

// sendEnterKey and sendUndoInput are indirected so tests can observe the
// multi-tap dispatch without injecting real keystrokes.
var sendEnterKey = autotype.SendEnter

var sendUndoInput = func(action string, logger *slog.Logger) error {
	if action == undoTapActionClear {
		return autotype.SendClearInput(logger)
	}
	return autotype.SendUndo(logger)
}

var TUILog func(string)
var TUILogBuf []string
var tuilogMu sync.Mutex
var outputMu sync.Mutex
var outputWriter io.Writer = os.Stdout
var tuiStatus = TUIVoiceStatus{State: "idle", UpdatedAt: time.Now()}
var tuiStatusMu sync.Mutex
var tuiStats TUIVoiceStats
var tuiStatsMu sync.Mutex

type TUIVoiceStatus struct {
	State           string
	Detail          string
	Recording       bool
	Stopping        bool
	StopAt          time.Time
	ErrorUntil      time.Time
	UpdatedAt       time.Time
	SessionID       uint64
	PendingFinishes int
	LastHotkeyAt    time.Time
	LastHotkeyType  string
	LastHandledAt   time.Time
	LastHandledType string
	QueuedHotkeys   uint64
	HandledHotkeys  uint64
	EventQueueLen   int
	EnterUntil      time.Time
	UndoUntil       time.Time
	// PartialText is the most recent non-final ASR text returned by the
	// streaming backend. It is cleared once a final result is published
	// or when the recording session ends. Only meaningful while state
	// == "recording".
	PartialText string
	// LevelSamples is a copy of the most recent recorder amplitude
	// samples in chronological order (oldest first, newest last). Each
	// sample is a normalized peak in [0,1]. Used by the overlay to
	// render a waveform next to the REC indicator.
	LevelSamples []float32
}

type TUIVoiceStats struct {
	Sessions          uint64
	Chars             uint64
	AudioDuration     time.Duration
	LastTextChars     int
	LastAudioDuration time.Duration
}

func pout(format string, args ...interface{}) {
	msg := strings.ReplaceAll(fmt.Sprintf(format, args...), "\n", " ")
	if TUILog != nil {
		TUILog(msg)
		return
	}
	outputMu.Lock()
	defer outputMu.Unlock()
	if outputWriter != nil {
		fmt.Fprint(outputWriter, msg)
	}
}

func SetOutput(w io.Writer) {
	outputMu.Lock()
	outputWriter = w
	outputMu.Unlock()
}

func SetupTUILog() {
	TUILog = func(msg string) {
		tuilogMu.Lock()
		TUILogBuf = append(TUILogBuf, msg)
		if len(TUILogBuf) > 200 {
			TUILogBuf = TUILogBuf[len(TUILogBuf)-100:]
		}
		tuilogMu.Unlock()
	}
}

func DisableTUILog() {
	tuilogMu.Lock()
	TUILog = nil
	TUILogBuf = nil
	tuilogMu.Unlock()
}

func TUIStats() TUIVoiceStats {
	tuiStatsMu.Lock()
	defer tuiStatsMu.Unlock()
	return tuiStats
}

func recordTUIStats(text string, audioDuration time.Duration) {
	chars := countTextRunes(text)
	tuiStatsMu.Lock()
	tuiStats.Sessions++
	tuiStats.Chars += uint64(chars)
	tuiStats.AudioDuration += audioDuration
	tuiStats.LastTextChars = chars
	tuiStats.LastAudioDuration = audioDuration
	snapshot := tuiStats
	tuiStatsMu.Unlock()
	go saveTUIStats(snapshot)
}

func countTextRunes(text string) int {
	n := 0
	for _, r := range text {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

type persistedStats struct {
	Sessions        uint64 `json:"sessions"`
	Chars           uint64 `json:"chars"`
	AudioDurationMs int64  `json:"audio_duration_ms"`
}

func loadTUIStats() {
	data, err := os.ReadFile(statsPath())
	if err != nil {
		return
	}
	var ps persistedStats
	if json.Unmarshal(data, &ps) != nil {
		return
	}
	tuiStatsMu.Lock()
	tuiStats.Sessions = ps.Sessions
	tuiStats.Chars = ps.Chars
	tuiStats.AudioDuration = time.Duration(ps.AudioDurationMs) * time.Millisecond
	tuiStats.LastTextChars = 0
	tuiStats.LastAudioDuration = 0
	tuiStatsMu.Unlock()
}

func saveTUIStats(stats TUIVoiceStats) {
	path := statsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	ps := persistedStats{
		Sessions:        stats.Sessions,
		Chars:           stats.Chars,
		AudioDurationMs: stats.AudioDuration.Milliseconds(),
	}
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

func statsPath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, ".local", "state")
		} else {
			base = "."
		}
	}
	return filepath.Join(base, "just-talk", "stats.json")
}

func TUIStatus() TUIVoiceStatus {
	tuiStatusMu.Lock()
	defer tuiStatusMu.Unlock()
	if tuiStatus.State == "error" && !tuiStatus.ErrorUntil.IsZero() && time.Now().After(tuiStatus.ErrorUntil) {
		tuiStatus.State = "idle"
		tuiStatus.Detail = "等待热键"
		tuiStatus.Recording = false
		tuiStatus.Stopping = false
		tuiStatus.StopAt = time.Time{}
		tuiStatus.ErrorUntil = time.Time{}
		tuiStatus.UpdatedAt = time.Now()
	}
	if tuiStatus.State == "enter" && !tuiStatus.EnterUntil.IsZero() && time.Now().After(tuiStatus.EnterUntil) {
		tuiStatus.State = "idle"
		tuiStatus.Detail = "等待热键"
		tuiStatus.Recording = false
		tuiStatus.Stopping = false
		tuiStatus.StopAt = time.Time{}
		tuiStatus.EnterUntil = time.Time{}
		tuiStatus.UpdatedAt = time.Now()
	}
	if tuiStatus.State == "undo" && !tuiStatus.UndoUntil.IsZero() && time.Now().After(tuiStatus.UndoUntil) {
		tuiStatus.State = "idle"
		tuiStatus.Detail = "等待热键"
		tuiStatus.Recording = false
		tuiStatus.Stopping = false
		tuiStatus.StopAt = time.Time{}
		tuiStatus.UndoUntil = time.Time{}
		tuiStatus.UpdatedAt = time.Now()
	}
	return tuiStatus
}

func setTUIStatus(update func(*TUIVoiceStatus)) {
	tuiStatusMu.Lock()
	defer tuiStatusMu.Unlock()
	update(&tuiStatus)
	tuiStatus.UpdatedAt = time.Now()
}

func markTUIHotkey(evt hotkey.Event) {
	tuiStatusMu.Lock()
	defer tuiStatusMu.Unlock()
	tuiStatus.LastHotkeyAt = time.Now()
	tuiStatus.LastHotkeyType = evt.Type.String()
	tuiStatus.UpdatedAt = time.Now()
}

func markTUIQueued(evt hotkey.Event, queueLen int) {
	tuiStatusMu.Lock()
	defer tuiStatusMu.Unlock()
	tuiStatus.LastHotkeyAt = time.Now()
	tuiStatus.LastHotkeyType = evt.Type.String()
	tuiStatus.QueuedHotkeys++
	tuiStatus.EventQueueLen = queueLen
	tuiStatus.UpdatedAt = time.Now()
}

func markTUIHandled(evt hotkey.Event) {
	tuiStatusMu.Lock()
	defer tuiStatusMu.Unlock()
	tuiStatus.LastHandledAt = time.Now()
	tuiStatus.LastHandledType = evt.Type.String()
	tuiStatus.HandledHotkeys++
	tuiStatus.UpdatedAt = time.Now()
}

type VoicePlugin struct {
	env                    engine.PluginEnv
	logger                 *slog.Logger
	cfg                    *config.Config
	mu                     sync.Mutex
	eventOnce              sync.Once
	events                 chan hotkey.Event
	combo                  hotkey.Combo
	mode                   string
	recording              bool
	stopping               bool
	holdReleased           bool
	userStopped            bool
	stopTimer              *time.Timer
	stopAt                 time.Time
	startedAt              time.Time
	sessionID              uint64
	sessionGen             uint64
	recorder               *Recorder
	asrClient              ASRBackend
	asrCancel              context.CancelFunc
	autoSubmit             bool
	stopDelayMs            int
	pendingDone            int
	outputInFlight         int
	finishingSessions      map[uint64]struct{}
	canceledSessions       map[uint64]struct{}
	outputSessions         map[uint64]struct{}
	errorUntil             time.Time
	errorTimer             *time.Timer
	lastError              string
	flowActive             bool
	cancelHotkeyRegistered bool
	retryHotkeyRegistered  bool
	doubleTapSend          bool
	multiTapWindow         time.Duration
	tapCount               int
	lastTapAt              time.Time
	tapKeyDown             bool
	enterUntil             time.Time
	tripleTapUndo          bool
	undoAction             string
	doubleTimer            *time.Timer
	undoUntil              time.Time
	// asrFinalTimeout overrides defaultASRFinalTimeout when non-zero. Tests
	// set it small to exercise the no-final paths without waiting 15s.
	asrFinalTimeout time.Duration
	// asrNoSpeechTimeout overrides defaultASRNoSpeechTimeout when non-zero.
	asrNoSpeechTimeout time.Duration
}

type recordingSession struct {
	sessionID   uint64
	recorder    *Recorder
	asrClient   ASRBackend
	asrCancel   context.CancelFunc
	autoSubmit  bool
	userStopped bool
	startedAt   time.Time
}

func NewVoicePlugin() *VoicePlugin {
	return &VoicePlugin{
		stopDelayMs:        defaultStopDelayMs,
		asrFinalTimeout:    defaultASRFinalTimeout,
		asrNoSpeechTimeout: defaultASRNoSpeechTimeout,
	}
}
func (p *VoicePlugin) Name() string    { return "voice" }
func (p *VoicePlugin) Version() string { return "0.6.0" }

func (p *VoicePlugin) Init(env engine.PluginEnv) error {
	p.env = env
	p.logger = env.Logger()
	p.cfg = env.Config()
	loadTUIStats()
	return p.registerFromConfig(env.Config())
}

func (p *VoicePlugin) Start(ctx context.Context) error {
	p.logger.Info("voice plugin started", "mode", p.mode)
	p.startEventWorker(ctx)
	p.mu.Lock()
	p.publishStatusLocked()
	p.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (p *VoicePlugin) Stop() error {
	p.mu.Lock()
	session := p.detachRecordingLocked()
	p.trackFinishLocked(session)
	p.cancelDoubleTimerLocked()
	p.publishStatusLocked()
	p.mu.Unlock()
	p.finishRecordingSession(session)
	return nil
}

func (p *VoicePlugin) OnConfigReload(cfg *config.Config) error { return p.registerFromConfig(cfg) }

func (p *VoicePlugin) registerFromConfig(cfg *config.Config) error {
	vc := cfg.Voice
	if !vc.Enabled {
		p.mu.Lock()
		oldCombo := p.combo
		p.cleanupTransientHotkeysLocked()
		p.cancelDoubleTimerLocked()
		p.combo = hotkey.Combo{}
		p.tapCount = 0
		p.tapKeyDown = false
		p.mu.Unlock()
		if oldCombo.Key != hotkey.KeyNone || oldCombo.Mods != hotkey.ModNone {
			p.env.UnregisterHotkey(oldCombo)
		}
		p.logger.Info("voice disabled")
		return nil
	}
	combo, err := config.ParseHotkey(vc.PushToTalk)
	if err != nil {
		return fmt.Errorf("parse hotkey %q: %w", vc.PushToTalk, err)
	}
	mode, err := config.NormalizeMode(vc.Mode)
	if err != nil {
		return err
	}
	if err := validateVoiceHotkey(combo); err != nil {
		return err
	}

	stopDelayMs := vc.StopDelayMs
	if stopDelayMs <= 0 {
		stopDelayMs = defaultStopDelayMs
	}
	multiTapMs := vc.MultiTapMs
	if multiTapMs <= 0 {
		multiTapMs = defaultMultiTapMs
	}
	multiTapWindow := time.Duration(multiTapMs) * time.Millisecond
	// Keep the multi-tap window comfortably inside the stop delay so the
	// accidental recording started by the first tap is still active (and
	// cancelable) when the gesture completes. The margin also stops a
	// confirmed double-tap from racing the stop-delay timer.
	if stopDelay := time.Duration(stopDelayMs) * time.Millisecond; multiTapWindow > stopDelay-150*time.Millisecond {
		multiTapWindow = stopDelay - 150*time.Millisecond
	}
	if multiTapWindow < 100*time.Millisecond {
		multiTapWindow = 100 * time.Millisecond
	}
	undoAction, err := config.NormalizeUndoAction(vc.UndoAction)
	if err != nil {
		return err
	}

	p.mu.Lock()
	oldCombo := p.combo
	oldMode := p.mode
	sameRegistration := oldCombo == combo && oldMode == mode
	p.combo = combo
	p.mode = mode
	p.autoSubmit = vc.AutoSubmit
	p.stopDelayMs = stopDelayMs
	p.doubleTapSend = vc.DoubleTapSend
	p.multiTapWindow = multiTapWindow
	p.tripleTapUndo = vc.TripleTapUndo
	p.undoAction = undoAction
	p.cancelDoubleTimerLocked()
	p.tapCount = 0
	p.tapKeyDown = false
	p.mu.Unlock()

	p.logger.Info("config_reloaded", "hotkey", combo, "mode", mode,
		"auto_submit", vc.AutoSubmit, "stop_delay_ms", stopDelayMs,
		"double_tap_send", vc.DoubleTapSend, "triple_tap_undo", vc.TripleTapUndo,
		"undo_action", undoAction, "multi_tap_ms", multiTapMs)

	if !sameRegistration {
		isOld := oldCombo.Key != hotkey.KeyNone || oldCombo.Mods != hotkey.ModNone
		if isOld {
			p.env.UnregisterHotkey(oldCombo)
		}
		opts := hotkey.RegisterOptions{Suppress: mode == "hold"}
		if err := p.env.RegisterHotkeyWithOptions(combo, opts, p.onHotkey); err != nil {
			return fmt.Errorf("register hotkey: %w", err)
		}
		p.logger.Info("hotkey_registered", "combo", combo, "suppress", opts.Suppress, "mode", mode)
	}
	return nil
}

func (p *VoicePlugin) onHotkey(evt hotkey.Event) {
	markTUIHotkey(evt)
	p.logger.Debug("voice hotkey received", "type", evt.Type, "combo", evt.Combo)
	p.startEventWorker(p.env.Engine().Context())
	// KeyUp is queued even in toggle mode: the event loop ignores it for
	// recording, but triple-tap detection needs the release edge to tell a
	// genuine re-press apart from keyboard auto-repeat.
	p.events <- evt
	markTUIQueued(evt, len(p.events))
	p.logger.Debug("voice hotkey queued", "type", evt.Type, "queue_len", len(p.events))
}

func (p *VoicePlugin) onCancelHotkey(evt hotkey.Event) {
	if evt.Type != hotkey.KeyDown {
		return
	}
	p.cancelRecording()
}

func (p *VoicePlugin) onRetryHotkey(evt hotkey.Event) {
	if evt.Type != hotkey.KeyDown {
		return
	}
	p.retryLastError()
}

func (p *VoicePlugin) startEventWorker(ctx context.Context) {
	p.eventOnce.Do(func() {
		p.events = make(chan hotkey.Event, 256)
		go p.eventLoop(ctx)
	})
}

func (p *VoicePlugin) eventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-p.events:
			p.handleHotkey(evt)
		}
	}
}

func (p *VoicePlugin) handleHotkey(evt hotkey.Event) {
	markTUIHandled(evt)

	gesture := tapNone
	p.mu.Lock()
	if evt.Type == hotkey.KeyDown {
		repeat := p.tapKeyDown
		p.tapKeyDown = true
		if !repeat {
			gesture = p.noteTapLocked(time.Now())
		}
	} else {
		p.tapKeyDown = false
	}
	mode, rec, stopping := p.mode, p.recording, p.stopping
	p.mu.Unlock()

	if gesture == tapTriple {
		p.logger.Debug("voice triple-tap detected: sending retract")
		p.triggerUndoSend()
		return
	}
	// A double-tap (Enter) is armed by noteTapLocked but only fires from a
	// timer once the multi-tap window closes, so the taps below still get
	// their normal hold/toggle handling until then.

	p.logger.Debug("voice hotkey handling", "type", evt.Type, "mode", mode, "recording", rec, "stopping", stopping)
	switch mode {
	case "hold":
		if evt.Type == hotkey.KeyDown {
			p.clearHoldReleased()
			if stopping {
				p.cancelStopDelay()
			} else if !rec {
				p.startRecording()
			}
		} else if evt.Type == hotkey.KeyUp {
			p.markHoldReleased()
		}
	case "toggle":
		if evt.Type != hotkey.KeyDown {
			return
		}
		if !rec {
			p.startRecording()
		} else if p.stopping {
			// Pressing again while the stop delay is still running means
			// "I am not done yet": cancel the pending stop and keep the
			// same session recording, exactly like hold mode does.
			//
			// This must NOT commit the session. The previous behaviour
			// (finish the in-flight session, then start a fresh one)
			// dispatched the transcript of the first part *and* recorded
			// the rest, so a single utterance could be pasted twice.
			p.cancelStopDelay()
		} else {
			p.startStopDelay()
		}
	}
}

// tapGesture is the resolved meaning of a completed multi-tap sequence.
type tapGesture int

const (
	tapNone tapGesture = iota
	tapDouble
	tapTriple
)

// noteTapLocked records a hotkey press and reports whether it completes a
// double- or triple-tap gesture. The caller must hold p.mu.
//
// A gesture only starts from an idle plugin: if a recording (or its finish
// work) is already in flight, presses are left to the normal hold/toggle
// handling. Once a gesture has started, further presses inside the window
// extend it; a slow re-press restarts the count, which naturally requires the
// plugin to be idle again at that point.
//
// A double-tap cannot be reported immediately, because a third tap may still
// turn it into a triple-tap. It is therefore armed as a timer that fires when
// the window closes (see fireDoubleTap).
func (p *VoicePlugin) noteTapLocked(now time.Time) tapGesture {
	if p.multiTapWindow <= 0 || (!p.doubleTapSend && !p.tripleTapUndo) {
		return tapNone
	}
	idle := !p.recording && !p.stopping && p.pendingDone == 0 && p.outputInFlight == 0 &&
		!(p.lastError != "" && now.Before(p.errorUntil))
	if p.tapCount == 0 {
		if !idle {
			return tapNone
		}
		p.tapCount = 1
		p.lastTapAt = now
		return tapNone
	}
	if now.Sub(p.lastTapAt) > p.multiTapWindow {
		p.cancelDoubleTimerLocked()
		p.tapCount = 0
		return p.noteTapLocked(now)
	}
	p.lastTapAt = now
	p.tapCount++
	switch {
	case p.tapCount == 2:
		// Double tap means Enter. It cannot fire yet because a third tap
		// would turn it into the retract gesture, so arm a timer instead.
		if p.doubleTapSend {
			p.armDoubleTapLocked()
		}
		return tapNone
	case p.tapCount >= 3:
		p.cancelDoubleTimerLocked()
		p.tapCount = 0
		if p.tripleTapUndo {
			return tapTriple
		}
		return tapNone
	}
	return tapNone
}

// armDoubleTapLocked schedules the Enter action for when the multi-tap window
// closes without a third press.
func (p *VoicePlugin) armDoubleTapLocked() {
	p.cancelDoubleTimerLocked()
	if p.multiTapWindow <= 0 {
		return
	}
	p.doubleTimer = time.AfterFunc(p.multiTapWindow, p.fireDoubleTap)
}

func (p *VoicePlugin) cancelDoubleTimerLocked() {
	if p.doubleTimer != nil {
		p.doubleTimer.Stop()
		p.doubleTimer = nil
	}
}

// fireDoubleTap runs once the double-tap is confirmed (no third tap arrived).
// It discards the recording started by the first tap, flashes the overlay
// Enter hint, and presses Enter in the focused window.
func (p *VoicePlugin) fireDoubleTap() {
	p.mu.Lock()
	if p.tapCount != 2 {
		// A third tap completed a triple, or the sequence was reset.
		p.doubleTimer = nil
		p.mu.Unlock()
		return
	}
	p.doubleTimer = nil
	p.tapCount = 0
	session := p.detachRecordingLocked()
	p.enterUntil = time.Now().Add(gestureHintDuration)
	p.publishStatusLocked()
	p.mu.Unlock()

	if session != nil {
		go p.discardRecordingSession(session)
	}
	pout("↵ 发送回车")
	go func() {
		if err := sendEnterKey(p.logger); err != nil {
			pout("❌ 回车发送失败: %v", err)
		}
	}()
}

// triggerUndoSend discards any recording the earlier taps may have started,
// flashes the overlay retract hint, and injects the configured retract keys.
func (p *VoicePlugin) triggerUndoSend() {
	p.mu.Lock()
	p.cancelDoubleTimerLocked()
	session := p.detachRecordingLocked()
	action := p.undoAction
	p.undoUntil = time.Now().Add(gestureHintDuration)
	p.tapCount = 0
	p.publishStatusLocked()
	p.mu.Unlock()

	if session != nil {
		go p.discardRecordingSession(session)
	}
	pout("↶ 撤回输入")
	go func() {
		if err := sendUndoInput(action, p.logger); err != nil {
			pout("❌ 撤回失败: %v", err)
		}
	}()
}

// discardRecordingSession tears down a recording without dispatching its
// transcript. Used when a triple-tap cancels the recording started by the
// first tap of the gesture.
func (p *VoicePlugin) discardRecordingSession(session *recordingSession) {
	if session == nil {
		return
	}
	if session.asrCancel != nil {
		session.asrCancel()
	}
	if session.recorder != nil {
		_, _ = session.recorder.Stop()
	}
	if session.asrClient != nil {
		_ = session.asrClient.Close()
	}
}

func (p *VoicePlugin) startStopDelay() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopping || !p.recording {
		return
	}
	p.holdReleased = false
	p.stopping, p.userStopped = true, true
	delay := time.Duration(p.stopDelayMs) * time.Millisecond
	p.stopAt = time.Now().Add(delay)
	p.stopTimer = time.AfterFunc(delay, func() {
		p.stopRecordingAsync()
	})
	p.publishStatusLocked()
	pout("🎤 即将停止... (%dms 缓冲)", p.stopDelayMs)
}

func (p *VoicePlugin) cancelStopDelay() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelStopDelayLocked()
}

func (p *VoicePlugin) cancelStopDelayLocked() {
	p.cancelStopDelayOnlyLocked()
	p.holdReleased = false
	p.publishStatusLocked()
}

func (p *VoicePlugin) cancelStopDelayOnlyLocked() {
	if p.stopTimer != nil {
		p.stopTimer.Stop()
		p.stopTimer = nil
	}
	if p.stopping {
		pout("🎤 继续录音")
	}
	p.stopping = false
	p.stopAt = time.Time{}
}

func (p *VoicePlugin) clearHoldReleased() {
	p.mu.Lock()
	p.holdReleased = false
	p.mu.Unlock()
}

func (p *VoicePlugin) markHoldReleased() {
	p.mu.Lock()
	p.holdReleased = true
	shouldStop := p.recording && !p.stopping
	p.mu.Unlock()
	if shouldStop {
		p.startStopDelay()
	}
}

func (p *VoicePlugin) startRecording() {
	p.mu.Lock()
	p.cancelStopDelayOnlyLocked()
	if p.recording || p.asrClient != nil {
		p.mu.Unlock()
		pout("⚠️  已经在录音中")
		return
	}
	vc := p.cfg.Voice
	asrCfg := ASRConfig{AppKey: vc.AppKey, AccessKey: vc.AccessKey, ResourceID: vc.ResourceID, Language: vc.Language, Hotwords: vc.Hotwords}
	if asrCfg.ResourceID == "" {
		asrCfg.ResourceID = "volc.bigasr.sauc.duration"
	}
	if asrCfg.Language == "" {
		asrCfg.Language = "zh-CN"
	}
	var rec *Recorder
	if vc.Device != "" {
		rec = NewRecorderWithDevice(p.logger, vc.Device, vc.Gain)
	} else {
		rec = NewRecorder(p.logger, vc.Gain)
	}
	if err := rec.Start(); err != nil {
		p.sessionID++
		sessionID := p.sessionID
		p.publishErrorLocked("录音启动失败: "+shortError(err), sessionID)
		p.mu.Unlock()
		pout("❌ 录音启动失败: %v", err)
		return
	}
	pout("🎤 开始录音... (后端: %s)", rec.Backend())
	ctx, cancel := context.WithCancel(context.Background())
	p.sessionID++
	p.sessionGen++
	sessionID := p.sessionID
	sessionGen := p.sessionGen
	startedAt := time.Now()
	p.recorder, p.recording, p.userStopped = rec, true, false
	p.startedAt = startedAt
	p.stopping = false
	p.stopAt = time.Time{}
	p.clearErrorLocked()
	p.asrCancel = cancel
	shouldStopImmediately := p.mode == "hold" && p.holdReleased
	p.publishStatusLocked()
	p.mu.Unlock() // Release lock before slow WebSocket dial

	go p.connectASR(ctx, cancel, sessionID, sessionGen, rec, asrCfg)
	go p.publishLevels(ctx, rec)
	if shouldStopImmediately {
		p.startStopDelay()
	}
}

func (p *VoicePlugin) newBackend(asrCfg ASRConfig) ASRBackend {
	switch p.cfg.Voice.ASRBackend {
	case "offline":
		off := OfflineASRConfig{
			ModelDir: p.cfg.Voice.OfflineModelDir,
			Script:   p.cfg.Voice.OfflineScript,
		}
		// Sensible local defaults when the TUI leaves them empty.
		if off.ModelDir == "" && fileExists("/home/kali/sherpa-models/sense-voice/model.int8.onnx") {
			off.ModelDir = "/home/kali/sherpa-models/sense-voice"
		}
		if off.Script == "" && fileExists("/home/kali/dev/keliv2.0_view/scripts/asr_text.py") {
			off.Script = "/home/kali/dev/keliv2.0_view/scripts/asr_text.py"
		}
		return NewOfflineASRClient(off, p.logger)
	case "doubao_url":
		// "auc" file-URL endpoint. Uses a single x-api-key header (the
		// WebSocket streaming AppKey/AccessKey pair is not interchangeable).
		apiKey := p.cfg.Voice.DoubaoAPIKey
		if apiKey == "" {
			apiKey = asrCfg.AppKey
		}
		resourceID := p.cfg.Voice.DoubaoResourceID
		if resourceID == "" {
			resourceID = "volc.seedasr.auc"
		}
		return NewURLASRClient(URLASRConfig{
			APIKey:     apiKey,
			ResourceID: resourceID,
			Language:   p.cfg.Voice.Language,
			Hotwords:   p.cfg.Voice.Hotwords,
			SubmitURL:  p.cfg.Voice.DoubaoSubmitURL,
			QueryURL:   p.cfg.Voice.DoubaoQueryURL,
		}, p.logger)
	default:
		return NewASRClient(asrCfg, p.logger)
	}
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func (p *VoicePlugin) connectASR(ctx context.Context, cancel context.CancelFunc, sessionID, sessionGen uint64, rec *Recorder, asrCfg ASRConfig) {
	client := p.newBackend(asrCfg)
	if err := client.Connect(ctx); err != nil {
		wasCanceled := ctx.Err() != nil
		cancel()
		p.mu.Lock()
		currentSession := p.sessionGen == sessionGen
		if currentSession {
			rec.Stop()
			p.stopping, p.recorder, p.recording = false, nil, false
			p.stopAt = time.Time{}
			p.asrCancel = nil
			if !wasCanceled {
				p.publishErrorLocked("ASR 连接失败: "+asrConnectErrorDetail(err), sessionID)
			} else {
				p.publishStatusLocked()
			}
		}
		p.mu.Unlock()
		if currentSession && !wasCanceled {
			pout("❌ ASR 连接失败: %v", err)
		}
		return
	}
	pout("✅ ASR 已连接")

	p.mu.Lock()
	if p.sessionGen != sessionGen || !p.recording || p.recorder != rec {
		p.mu.Unlock()
		client.Close()
		cancel()
		return
	}
	p.asrClient = client
	p.publishStatusLocked()
	p.mu.Unlock()

	client.StartReceive(ctx)
	go p.streamAudio(ctx, rec, client)
	go func() {
		for result := range client.Results() {
			if result.Error != nil {
				pout("❌ ASR 错误: %v", result.Error)
				continue
			}
			if result.IsFinal {
				pout("\n🎤 最终: %s", result.Text)
				setTUIStatus(func(s *TUIVoiceStatus) {
					s.PartialText = ""
				})
			} else if result.Text != "" {
				pout("\r🎤 %s", result.Text)
				setTUIStatus(func(s *TUIVoiceStatus) {
					s.PartialText = result.Text
				})
			}
		}
	}()
}

func (p *VoicePlugin) cancelRecording() {
	p.mu.Lock()
	session := p.detachRecordingLocked()
	hadError := p.lastError != "" && time.Now().Before(p.errorUntil)
	hadPending := p.pendingDone > 0
	if hadPending {
		if p.canceledSessions == nil {
			p.canceledSessions = make(map[uint64]struct{})
		}
		for id := range p.finishingSessions {
			p.canceledSessions[id] = struct{}{}
		}
		p.pendingDone = 0
	}
	p.clearErrorLocked()
	p.publishStatusLocked()
	p.mu.Unlock()

	if session != nil {
		if session.asrCancel != nil {
			session.asrCancel()
		}
		if session.recorder != nil {
			_, _ = session.recorder.Stop()
		}
		if session.asrClient != nil {
			_ = session.asrClient.Close()
		}
		pout("🎤 已取消本次录音")
	} else if hadPending {
		pout("🎤 已取消等待识别结果")
	} else if hadError {
		pout("⚠️  已关闭错误状态")
	}
}

func (p *VoicePlugin) retryLastError() {
	p.mu.Lock()
	if p.recording || p.stopping || p.pendingDone > 0 || p.lastError == "" || time.Now().After(p.errorUntil) {
		p.mu.Unlock()
		return
	}
	p.clearErrorLocked()
	p.publishStatusLocked()
	p.mu.Unlock()
	p.startRecording()
}

func (p *VoicePlugin) stopRecordingAsync() {
	p.mu.Lock()
	session := p.detachRecordingLocked()
	p.trackFinishLocked(session)
	p.publishStatusLocked()
	p.mu.Unlock()
	if session != nil {
		go p.finishRecordingSession(session)
	}
}

func (p *VoicePlugin) detachRecordingLocked() *recordingSession {
	if p.stopTimer != nil {
		p.stopTimer.Stop()
		p.stopTimer = nil
	}
	p.stopAt = time.Time{}
	if p.recorder == nil && p.asrClient == nil && p.asrCancel == nil {
		p.recording, p.stopping, p.userStopped = false, false, false
		return nil
	}
	session := &recordingSession{
		sessionID:   p.sessionID,
		recorder:    p.recorder,
		asrClient:   p.asrClient,
		asrCancel:   p.asrCancel,
		autoSubmit:  p.autoSubmit,
		userStopped: p.userStopped,
		startedAt:   p.startedAt,
	}
	p.sessionGen++
	p.recorder, p.asrClient, p.asrCancel = nil, nil, nil
	p.startedAt = time.Time{}
	p.recording, p.stopping, p.userStopped = false, false, false
	p.holdReleased = false
	return session
}

func (p *VoicePlugin) trackFinishLocked(session *recordingSession) {
	if session == nil {
		return
	}
	p.pendingDone++
	if p.finishingSessions == nil {
		p.finishingSessions = make(map[uint64]struct{})
	}
	p.finishingSessions[session.sessionID] = struct{}{}
}

func (p *VoicePlugin) finishRecordingSession(session *recordingSession) {
	if session == nil {
		return
	}
	defer p.recordingSessionFinished(session.sessionID)
	pout("🎤 停止录音")
	if session.asrClient == nil && session.asrCancel != nil {
		session.asrCancel()
	}
	var remaining []byte
	capturedBytes := int64(0)
	sessionPeak := float32(0)
	if session.recorder != nil {
		p.logger.Debug("finish session: stopping recorder")
		remaining, _ = session.recorder.Stop()
		capturedBytes = session.recorder.CapturedBytes()
		sessionPeak = session.recorder.SessionPeak()
		p.logger.Debug("finish session: recorder stopped",
			"remaining_bytes", len(remaining), "captured_bytes", capturedBytes, "session_peak", sessionPeak)
	}

	// Skip the ASR round-trip in two cases:
	//
	//  1. No audio at all was captured (mic muted, hotkey released before any
	//     sample landed). This must key off the *total* bytes captured this
	//     session, not off len(remaining): streamAudio continuously drains the
	//     capture pipe while the user speaks, so Stop() normally returns only
	//     the unread tail (often zero) even for a long, loud utterance.
	//  2. Audio was captured but it never rose above silencePeak, i.e. nobody
	//     actually spoke. A microphone always produces PCM in a quiet room, so
	//     the byte count cannot see this; the session peak can.
	//
	// Both cases used to keep the overlay on WAI until the backend's final
	// wait expired, then flash ERR. Skipping the backend entirely makes a
	// no-speech recording return to idle immediately and keeps a stale
	// lastText from a previous session out of this session's output.
	noAudio := capturedBytes == 0 && len(remaining) == 0
	silentAudio := !noAudio && sessionPeak < silencePeak
	skipASR := noAudio || silentAudio

	if session.asrClient != nil {
		if !skipASR {
			p.logger.Debug("finish session: sending final audio", "bytes", len(remaining))
			sendCtx, sendCancel := context.WithTimeout(context.Background(), 3*time.Second)
			if err := session.asrClient.SendAudio(sendCtx, remaining, true); err != nil {
				p.logger.Warn("send final audio failed", "error", err)
			}
			sendCancel()
			p.logger.Debug("finish session: waiting ASR final")
			finalTimeout := p.asrFinalTimeout
			if finalTimeout <= 0 {
				finalTimeout = defaultASRFinalTimeout
			}
			noSpeechTimeout := p.asrNoSpeechTimeout
			if noSpeechTimeout <= 0 {
				noSpeechTimeout = defaultASRNoSpeechTimeout
			}
			// Keep the no-speech grace strictly inside the final timeout so
			// the two timers cannot race to an ambiguous outcome.
			if noSpeechTimeout >= finalTimeout {
				noSpeechTimeout = finalTimeout / 2
			}

			finalTimer := time.NewTimer(finalTimeout)
			defer finalTimer.Stop()
			graceTimer := time.NewTimer(noSpeechTimeout)
			defer graceTimer.Stop()

			waiting := true
			for waiting {
				select {
				case <-session.asrClient.Final():
					p.logger.Debug("finish session: ASR final received")
					waiting = false
				case <-session.asrClient.Done():
					p.logger.Debug("finish session: ASR done")
					waiting = false
				case <-graceTimer.C:
					// No partial hypothesis after the grace period: the
					// backend almost certainly heard nothing. Return to idle
					// quietly instead of holding WAI for the full timeout.
					if session.asrClient.LastText() == "" {
						if !p.sessionCanceled(session.sessionID) {
							p.logger.Debug("finish session: no partial within no-speech grace")
							pout("🎤 未检测到语音")
						}
						waiting = false
					}
					// Otherwise partials arrived: keep waiting for the final.
				case <-finalTimer.C:
					if !p.sessionCanceled(session.sessionID) {
						pout("⚠️  识别超时")
						p.publishError(fmt.Sprintf("识别超时: 等待 ASR final 超过 %s", finalTimeout), session.sessionID)
					}
					waiting = false
				}
			}
			if text := session.asrClient.LastText(); text != "" && session.userStopped && p.claimSessionOutput(session.sessionID) {
				audioDuration := time.Duration(0)
				if !session.startedAt.IsZero() {
					audioDuration = time.Since(session.startedAt)
				}
				recordTUIStats(text, audioDuration)
				p.dispatchTextOutput(text, session.autoSubmit)
			}
		} else {
			if silentAudio {
				p.logger.Debug("finish session: silent audio, skipping ASR round-trip",
					"session_peak", sessionPeak)
				pout("🎤 未检测到语音")
			} else {
				p.logger.Debug("finish session: empty audio, skipping ASR round-trip")
			}
		}
		p.logger.Debug("finish session: closing ASR client")
		closeDone := make(chan error, 1)
		go func() { closeDone <- session.asrClient.Close() }()
		select {
		case err := <-closeDone:
			if err != nil {
				p.logger.Debug("close ASR client failed", "error", err)
			}
		case <-time.After(2 * time.Second):
			p.logger.Warn("close ASR client timed out")
		}
	}
	if session.asrCancel != nil {
		session.asrCancel()
	}
	p.logger.Debug("finish session: done")
}

func (p *VoicePlugin) dispatchTextOutput(text string, autoSubmit bool) {
	// Track in-flight dispatch so the triple-tap Enter gesture does not fire
	// before the paste/Shift+Insert has actually landed in the focused field.
	p.mu.Lock()
	p.outputInFlight++
	p.mu.Unlock()
	go func() {
		defer func() {
			p.mu.Lock()
			if p.outputInFlight > 0 {
				p.outputInFlight--
			}
			p.mu.Unlock()
		}()
		if autoSubmit {
			if err := autotype.Paste(text, p.logger); err != nil {
				pout("❌ 上屏失败: %v", err)
			} else {
				pout("📋 已复制到剪贴板")
				pout("✅ 已上屏")
			}
			return
		}

		p.logger.Debug("text output: writing clipboard", "text_len", len(text))
		if err := writeClipboard(text); err != nil {
			pout("❌ 复制到剪贴板失败: %v", err)
		} else {
			pout("📋 已复制到剪贴板")
		}
	}()
}

func (p *VoicePlugin) recordingSessionFinished(sessionID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.finishingSessions, sessionID)
	delete(p.canceledSessions, sessionID)
	if p.pendingDone > 0 {
		p.pendingDone--
	}
	p.publishStatusLocked()
}

func (p *VoicePlugin) sessionCanceled(sessionID uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.canceledSessions[sessionID]
	return ok
}

func (p *VoicePlugin) claimSessionOutput(sessionID uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, canceled := p.canceledSessions[sessionID]; canceled {
		return false
	}
	if p.outputSessions == nil {
		p.outputSessions = make(map[uint64]struct{})
	}
	if _, exists := p.outputSessions[sessionID]; exists {
		return false
	}
	p.outputSessions[sessionID] = struct{}{}
	return true
}

func (p *VoicePlugin) publishStatusLocked() {
	state, detail := "idle", "等待热键"
	stopAt := time.Time{}
	recording, stopping := p.recording, false

	switch {
	case p.recording && p.stopping:
		state, detail = "stopping_delayed", "等待停止延迟"
		stopping = true
		stopAt = p.stopAt
	case p.recording && p.asrClient == nil && p.asrCancel != nil:
		state, detail = "connecting", "录音中，正在连接 ASR"
	case p.recording:
		state, detail = "recording", "录音中"
	case p.pendingDone > 0:
		state, detail = "stopping", "正在停止并等待识别结果"
		stopping = true
	case p.lastError != "" && time.Now().Before(p.errorUntil):
		state, detail = "error", p.lastError
	case time.Now().Before(p.enterUntil):
		state, detail = "enter", "已发送回车"
	case time.Now().Before(p.undoUntil):
		state, detail = "undo", "已撤回输入"
	}

	p.publishStatusSnapshotLocked(state, detail, recording, stopping, stopAt, p.sessionID)
	errorActive := state == "error" && time.Now().Before(p.errorUntil)
	wasFlowActive := p.flowActive
	p.flowActive = state != "idle"
	if state == "idle" && wasFlowActive {
		p.cleanupTransientHotkeysLocked()
		return
	}
	p.syncTransientHotkeysLocked(recording || stopping || p.pendingDone > 0 || errorActive, errorActive)
}

func (p *VoicePlugin) publishErrorLocked(detail string, sessionID uint64) {
	p.lastError = detail
	p.errorUntil = time.Now().Add(errorHoldDuration)
	if p.errorTimer != nil {
		p.errorTimer.Stop()
	}
	p.errorTimer = time.AfterFunc(errorHoldDuration, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.lastError != "" && time.Now().After(p.errorUntil) {
			p.clearErrorLocked()
			p.publishStatusLocked()
		}
	})
	p.publishStatusSnapshotLocked("error", detail, false, false, time.Time{}, sessionID)
	p.syncTransientHotkeysLocked(true, true)
}

func (p *VoicePlugin) publishError(detail string, sessionID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishErrorLocked(detail, sessionID)
}

func (p *VoicePlugin) publishStatusSnapshotLocked(state, detail string, recording, stopping bool, stopAt time.Time, sessionID uint64) {
	pendingDone := p.pendingDone
	setTUIStatus(func(s *TUIVoiceStatus) {
		s.State, s.Detail = state, detail
		s.Recording, s.Stopping = recording, stopping
		s.StopAt = stopAt
		s.ErrorUntil = p.errorUntil
		s.SessionID = sessionID
		s.PendingFinishes = pendingDone
		s.EnterUntil = p.enterUntil
		s.UndoUntil = p.undoUntil
	})
}

func (p *VoicePlugin) clearErrorLocked() {
	if p.errorTimer != nil {
		p.errorTimer.Stop()
		p.errorTimer = nil
	}
	p.errorUntil = time.Time{}
	p.lastError = ""
}

func (p *VoicePlugin) cleanupTransientHotkeysLocked() {
	p.syncTransientHotkeysLocked(false, false)
}

func validateVoiceHotkey(combo hotkey.Combo) error {
	if combo.Key.IsTextKey() {
		return fmt.Errorf("语音热键不支持普通字符键 %s；请使用适合作为全局快捷键的组合，如 Alt+Super、Alt+F8、F9、Ctrl+Alt+Tab", combo)
	}
	return nil
}

func (p *VoicePlugin) syncTransientHotkeysLocked(needCancel, needRetry bool) {
	// No-op when running under unit tests where env is not wired up.
	if p.env == nil {
		return
	}
	if needCancel && !p.cancelHotkeyRegistered {
		if err := p.env.RegisterHotkey(cancelRecordingCombo, p.onCancelHotkey); err != nil {
			p.logger.Warn("register cancel hotkey failed", "combo", cancelRecordingCombo, "error", err)
		} else {
			p.cancelHotkeyRegistered = true
		}
	} else if !needCancel && p.cancelHotkeyRegistered {
		if err := p.env.UnregisterHotkey(cancelRecordingCombo); err != nil {
			p.logger.Warn("unregister cancel hotkey failed", "combo", cancelRecordingCombo, "error", err)
		}
		p.cancelHotkeyRegistered = false
	}

	if needRetry && !p.retryHotkeyRegistered {
		if err := p.env.RegisterHotkey(retryErrorCombo, p.onRetryHotkey); err != nil {
			p.logger.Warn("register retry hotkey failed", "combo", retryErrorCombo, "error", err)
		} else {
			p.retryHotkeyRegistered = true
		}
	} else if !needRetry && p.retryHotkeyRegistered {
		if err := p.env.UnregisterHotkey(retryErrorCombo); err != nil {
			p.logger.Warn("unregister retry hotkey failed", "combo", retryErrorCombo, "error", err)
		}
		p.retryHotkeyRegistered = false
	}
}

func shortError(err error) string {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "未知错误"
	}
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len([]rune(msg)) <= 120 {
		return msg
	}
	return string([]rune(msg)[:120]) + "..."
}

func asrConnectErrorDetail(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "HTTP 403") {
		return "认证失败，请检查 App Key 或 Access Key。"
	}
	return shortError(err)
}

func (p *VoicePlugin) streamAudio(ctx context.Context, rec *Recorder, client ASRBackend) {
	buf := make([]byte, 6400)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := rec.Read(buf)
		if n > 0 {
			rec.recordLevel(buf[:n])
			client.SendAudio(ctx, buf[:n], false)
		}
		if err == io.EOF || (err != nil && ctx.Err() != nil) {
			return
		}
		if err != nil {
			p.logger.Error("read audio error", "error", err)
			return
		}
	}
}

// publishLevels copies the recorder's recent amplitude samples into
// TUIVoiceStatus at ~30 Hz so the overlay can render a live waveform
// alongside the REC indicator. The goroutine exits when ctx is
// cancelled (recording session ended or was cancelled) and clears
// the LevelSamples field so the overlay returns to its idle
// rendering.
func (p *VoicePlugin) publishLevels(ctx context.Context, rec *Recorder) {
	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()
	defer func() {
		setTUIStatus(func(s *TUIVoiceStatus) {
			s.LevelSamples = nil
		})
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			samples := rec.Levels()
			if rec.LevelCount() == 0 {
				continue
			}
			setTUIStatus(func(s *TUIVoiceStatus) {
				s.LevelSamples = samples
			})
		}
	}
}

func writeClipboard(text string) error {
	cb, err := clipboard.New()
	if err != nil {
		return err
	}
	return cb.Set(text)
}
