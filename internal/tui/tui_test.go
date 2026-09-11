package tui

import (
	"context"
	"testing"
	"time"

	"github.com/c/just-talk-go/config"
	"github.com/c/just-talk-go/hotkey"
)

// TestASRBackendFieldInTUI verifies the "识别引擎" selector field is rendered
// in the TUI field list and that saving through the TUI round-trips the
// asr_backend value into the config.
func TestASRBackendFieldInTUI(t *testing.T) {
	cfg := config.Default()
	m := New(cfg)

	var found *field
	var sawOnline, sawOffline, sawDoubaoURL bool
	for i := range m.fields {
		f := &m.fields[i]
		if f.key == "asr_backend" {
			found = f
		}
	}
	if found == nil {
		t.Fatal("asr_backend field not present in TUI, expected a 识别引擎 selector")
	}
	if found.fType != fSelect {
		t.Fatalf("asr_backend should be a select field, got fType=%d", found.fType)
	}
	for _, o := range found.opts {
		if o == "online" {
			sawOnline = true
		}
		if o == "offline" {
			sawOffline = true
		}
		if o == "doubao_url" {
			sawDoubaoURL = true
		}
	}
	if !sawOnline || !sawOffline || !sawDoubaoURL {
		t.Fatalf("asr_backend options should include online, doubao_url & offline, got %v", found.opts)
	}

	// Simulate the user navigating to offline and saving.
	// index 2 = "offline" given opts {"online","doubao_url","offline"}
	if len(found.opts) != 3 {
		t.Fatalf("unexpected option count %d", len(found.opts))
	}
	found.optIdx = 2 // offline
	m.save()
	if m.cfg.Voice.ASRBackend != "offline" {
		t.Fatalf("save() should persist asr_backend=offline, got %q", m.cfg.Voice.ASRBackend)
	}
	t.Logf("PASS: TUI 识别引擎 field present & offline selectable/saveable; default=%q current=%q", cfg.Voice.ASRBackend, m.cfg.Voice.ASRBackend)
}

// TestHotkeyFieldUsesCaptureType verifies the push_to_talk field renders with
// the capture-enabled field type so the user can record a combo without
// typing the textual representation.
func TestHotkeyFieldUsesCaptureType(t *testing.T) {
	cfg := config.Default()
	m := New(cfg)
	for i := range m.fields {
		f := &m.fields[i]
		if f.key == "push_to_talk" {
			if f.fType != fHotkey {
				t.Fatalf("push_to_talk should be fHotkey, got %d", f.fType)
			}
			return
		}
	}
	t.Fatal("push_to_talk field not found")
}

// TestLLMConfigNotInTUI verifies the LLM-related TUI fields are gone now
// that the voice plugin no longer consumes the [llm] config block. The
// config struct itself stays around so existing user config files keep
// loading without errors; only the UI was retired.
func TestLLMConfigNotInTUI(t *testing.T) {
	cfg := config.Default()
	m := New(cfg)
	for _, f := range m.fields {
		if f.key == "llm_enabled" || f.key == "llm_base_url" || f.key == "llm_model" ||
			f.key == "llm_api_key" || f.key == "llm_system_prompt" || f.key == "llm_timeout_ms" {
			t.Fatalf("LLM TUI field %q should be removed", f.key)
		}
	}
}

// TestCaptureResultUpdatesField verifies that a captureResultMsg correctly
// updates the field whose key matches the captureKey, while leaving other
// fields untouched.
func TestCaptureResultUpdatesField(t *testing.T) {
	cfg := config.Default()
	cfg.Voice.PushToTalk = "Alt+Super"
	m := New(cfg)
	m.captureKey = "push_to_talk"
	_, _ = m.Update(captureResultMsg{combo: hotkey.Combo{Mods: hotkey.ModCtrl | hotkey.ModAlt, Key: hotkey.KeyF8}})

	var got string
	for _, f := range m.fields {
		if f.key == "push_to_talk" {
			got = f.input.Value()
		}
	}
	if got != "Ctrl+Alt+F8" {
		t.Fatalf("expected field value=Ctrl+Alt+F8, got %q", got)
	}
	if m.capturing {
		t.Fatal("capturing flag should be cleared after captureResultMsg")
	}
	if m.captureKey != "" {
		t.Fatalf("captureKey should be cleared, got %q", m.captureKey)
	}
}

// TestCaptureResultRejectsTextKeys verifies that captureResultMsg rejects
// combos that are text keys (e.g. Ctrl+A), which the voice plugin would
// refuse to register.
func TestCaptureResultRejectsTextKeys(t *testing.T) {
	cfg := config.Default()
	cfg.Voice.PushToTalk = "Alt+Super"
	m := New(cfg)
	m.captureKey = "push_to_talk"
	_, _ = m.Update(captureResultMsg{combo: hotkey.Combo{Mods: hotkey.ModCtrl, Key: hotkey.KeyA}})
	for _, f := range m.fields {
		if f.key == "push_to_talk" {
			if f.input.Value() != "Alt+Super" {
				t.Fatalf("text key combo should not overwrite valid hotkey; got %q", f.input.Value())
			}
		}
	}
}

// TestStartCaptureTimeoutReturnsErrorMsg verifies that a context-timeout
// capture is surfaced as an error message rather than silently dropping
// the user input.
func TestStartCaptureTimeoutReturnsErrorMsg(t *testing.T) {
	cfg := config.Default()
	m := New(cfg)
	m.OnCapture = func(ctx context.Context) (hotkey.Combo, error) {
		<-ctx.Done()
		return hotkey.Combo{}, ctx.Err()
	}
	m.editing = true
	m.cursor = 0
	for i, f := range m.fields {
		if f.key == "push_to_talk" {
			m.cursor = i
			break
		}
	}
	cmd := m.startCapture("push_to_talk")
	if cmd == nil {
		t.Fatal("startCapture should return a non-nil tea.Cmd")
	}
	if !m.capturing {
		t.Fatal("capturing should be true after startCapture")
	}
	msg := cmd()
	res, ok := msg.(captureResultMsg)
	if !ok {
		t.Fatalf("expected captureResultMsg, got %T", msg)
	}
	if res.err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// TestStartCaptureFiresOnCaptureCallback verifies that startCapture wires
// the OnCapture callback through to the returned tea.Cmd.
func TestStartCaptureFiresOnCaptureCallback(t *testing.T) {
	cfg := config.Default()
	m := New(cfg)
	want := hotkey.Combo{Mods: hotkey.ModSuper, Key: hotkey.KeyF9}
	m.OnCapture = func(ctx context.Context) (hotkey.Combo, error) {
		return want, nil
	}
	cmd := m.startCapture("push_to_talk")
	if cmd == nil {
		t.Fatal("startCapture returned nil")
	}
	msg := cmd()
	res, ok := msg.(captureResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("expected captureResultMsg without error, got %T %+v", msg, res)
	}
	if res.combo != want {
		t.Fatalf("combo mismatch: got %s want %s", res.combo, want)
	}
	// Ensure the deadline is generous enough not to trip in unit tests.
	_ = time.Now()
}
