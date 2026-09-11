package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/c/just-talk-go/hotkey"
)

type Config struct {
	Voice   VoiceConfig   `toml:"voice"`
	Debug   DebugConfig   `toml:"debug"`
	Overlay OverlayConfig `toml:"overlay"`
	LLM     LLMConfig     `toml:"llm"`
}

type DebugConfig struct {
	Enabled bool     `toml:"enabled"`
	Hotkeys []string `toml:"hotkeys"`
}

type OverlayConfig struct {
	Enabled     bool    `toml:"enabled"`
	Position    string  `toml:"position"`
	IdleVisible bool    `toml:"idle_visible"`
	Scale       float64 `toml:"scale"`
}

type VoiceConfig struct {
	Enabled     bool   `toml:"enabled"`
	Mode        string `toml:"mode"`
	PushToTalk  string `toml:"push_to_talk"`
	Device      string `toml:"device"`
	Gain        int    `toml:"gain"`
	StopDelayMs int    `toml:"stop_delay_ms"`
	Language    string `toml:"language"`
	AutoSubmit  bool   `toml:"auto_submit"`
	// ASRBackend selects the recognition engine: "online" (Doubao/Volcano
	// streaming WebSocket, the default) or "offline" (local sherpa-onnx
	// SenseVoice). The latter works with no network.
	ASRBackend string `toml:"asr_backend"`
	// OfflineModelDir optionally overrides the local SenseVoice model dir for
	// offline mode. Empty uses asr_text.py's default (/root/sherpa-models/sense-voice).
	OfflineModelDir string `toml:"offline_model_dir"`
	// OfflineScript optionally points at the asr_text.py helper. Empty
	// auto-discovers common locations.
	OfflineScript string   `toml:"offline_script"`
	AppKey        string   `toml:"app_key"`
	AccessKey     string   `toml:"access_key"`
	ResourceID    string   `toml:"resource_id"`
	Hotwords      []string `toml:"hotwords"`
	// DoubaoAPIKey is the single-token credential used by the doubao_url
	// backend (volc.seedasr.auc submit/query). It is separate from
	// AppKey/AccessKey because the streaming WebSocket and the file-URL
	// endpoint use different auth schemes.
	DoubaoAPIKey     string `toml:"doubao_api_key"`
	DoubaoResourceID string `toml:"doubao_resource_id"`
	// DoubaoSubmitURL / DoubaoQueryURL override the default Doubao file
	// recognition endpoints. Leave empty to use the public defaults at
	// openspeech.bytedance.com. Useful for the China mainland gateway
	// (openspeech.bytedance.com), or when proxying through a private
	// deployment.
	DoubaoSubmitURL string `toml:"doubao_submit_url"`
	DoubaoQueryURL  string `toml:"doubao_query_url"`
	// DoubleTapSend enables the "quickly tap the voice hotkey twice while
	// idle to press Enter" gesture. Enter is the most frequent action, so it
	// gets the easier double tap.
	DoubleTapSend bool `toml:"double_tap_send"`
	// TripleTapUndo enables the "quickly tap the voice hotkey three times
	// while idle to retract the just-pasted text" gesture.
	TripleTapUndo bool `toml:"triple_tap_undo"`
	// UndoAction selects what the triple-tap retract sends: "undo" presses
	// Ctrl+Z, "clear" selects all and deletes (Ctrl+A then Backspace).
	// Empty uses "undo".
	UndoAction string `toml:"undo_action"`
	// MultiTapMs is the maximum gap (milliseconds) allowed between
	// consecutive hotkey taps for them to count as one multi-tap gesture
	// (double or triple). The window is clamped below StopDelayMs so a
	// gesture always completes before the recording stop delay fires.
	// Empty/zero uses the default.
	MultiTapMs int `toml:"multi_tap_ms"`
}

// LLMConfig configures an optional OpenAI-compatible chat backend used to
// polish ASR output before it is pasted into the focused field. When
// Enabled is false or any required field is empty, the voice plugin falls
// back to dispatching the raw ASR text and surfaces a one-line warning.
type LLMConfig struct {
	Enabled      bool   `toml:"enabled"`
	BaseURL      string `toml:"base_url"`
	Model        string `toml:"model"`
	APIKey       string `toml:"api_key"`
	SystemPrompt string `toml:"system_prompt"`
	TimeoutMs    int    `toml:"timeout_ms"`
}

func Default() *Config {
	return &Config{
		Voice: VoiceConfig{
			Enabled: true, Mode: "toggle", PushToTalk: "Alt+Super",
			Language: "zh-CN", AutoSubmit: true, ResourceID: "volc.bigasr.sauc.duration",
			ASRBackend: "online", DoubleTapSend: true, TripleTapUndo: true,
			MultiTapMs: 500, UndoAction: "undo",
		},
		Overlay: OverlayConfig{
			Enabled: true, Position: "bottom-center", IdleVisible: false, Scale: 1.0,
		},
		LLM: LLMConfig{
			Enabled:   false,
			BaseURL:   "https://api.openai.com/v1",
			Model:     "gpt-4o-mini",
			TimeoutMs: 8000,
			SystemPrompt: "You rewrite ASR transcripts for a desktop voice-input tool. " +
				"Fix obvious speech-recognition errors, remove filler words (um, uh, 嗯, 啊, 那个), " +
				"and produce fluent written text in the same language as the input. " +
				"Preserve the user's intent and named entities. Output only the rewritten text, no commentary.",
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		path = FindConfig()
	}
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

func FindConfig() string {
	candidates := []string{"./config.toml"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "just-talk", "config.toml"))
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "just-talk", "config.toml"))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func Save(cfg *Config) error {
	path := FindConfig()
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "just-talk", "config.toml")
		os.MkdirAll(filepath.Dir(path), 0755)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// ---- Hotkey parser ----

var modifierNames = map[string]hotkey.Modifier{
	"ctrl": hotkey.ModCtrl, "alt": hotkey.ModAlt, "shift": hotkey.ModShift,
	"control": hotkey.ModCtrl, "option": hotkey.ModAlt, "super": hotkey.ModSuper,
	"cmd": hotkey.ModSuper, "command": hotkey.ModSuper, "win": hotkey.ModSuper,
}

var keyNameToCode = buildKeyNameMap()

func buildKeyNameMap() map[string]hotkey.KeyCode {
	m := make(map[string]hotkey.KeyCode)
	for i := hotkey.KeyA; i <= hotkey.KeyZ; i++ {
		m[strings.ToLower(i.String())] = i
		m[i.String()] = i
	}
	for i := hotkey.Key0; i <= hotkey.Key9; i++ {
		m[i.String()] = i
	}
	for i := hotkey.KeyF1; i <= hotkey.KeyF24; i++ {
		m[strings.ToLower(i.String())] = i
		m[i.String()] = i
	}
	m["ctrl"] = hotkey.KeyCtrl
	m["control"] = hotkey.KeyCtrl
	m["alt"] = hotkey.KeyAlt
	m["option"] = hotkey.KeyAlt
	m["shift"] = hotkey.KeyShift
	m["super"] = hotkey.KeySuper
	m["cmd"] = hotkey.KeySuper
	m["command"] = hotkey.KeySuper
	m["win"] = hotkey.KeySuper
	for _, k := range []hotkey.KeyCode{
		hotkey.KeySpace, hotkey.KeyTab, hotkey.KeyEnter, hotkey.KeyEscape,
		hotkey.KeyBackspace, hotkey.KeyCapsLock,
		hotkey.KeyArrowUp, hotkey.KeyArrowDown, hotkey.KeyArrowLeft, hotkey.KeyArrowRight,
		hotkey.KeyHome, hotkey.KeyEnd, hotkey.KeyPageUp, hotkey.KeyPageDown,
		hotkey.KeyInsert, hotkey.KeyDelete,
		hotkey.KeyNum0, hotkey.KeyNum1, hotkey.KeyNum2, hotkey.KeyNum3, hotkey.KeyNum4,
		hotkey.KeyNum5, hotkey.KeyNum6, hotkey.KeyNum7, hotkey.KeyNum8, hotkey.KeyNum9,
		hotkey.KeyBacktick, hotkey.KeyMinus, hotkey.KeyEqual,
		hotkey.KeyLeftBracket, hotkey.KeyRightBracket, hotkey.KeyBackslash,
		hotkey.KeySemicolon, hotkey.KeyQuote,
		hotkey.KeyComma, hotkey.KeyPeriod, hotkey.KeySlash,
	} {
		m[strings.ToLower(k.String())] = k
		m[k.String()] = k
	}
	m["space"] = hotkey.KeySpace
	m["enter"] = hotkey.KeyEnter
	m["return"] = hotkey.KeyEnter
	m["esc"] = hotkey.KeyEscape
	m["escape"] = hotkey.KeyEscape
	m["backspace"] = hotkey.KeyBackspace
	m["tab"] = hotkey.KeyTab
	m["up"] = hotkey.KeyArrowUp
	m["down"] = hotkey.KeyArrowDown
	m["left"] = hotkey.KeyArrowLeft
	m["right"] = hotkey.KeyArrowRight
	m["home"] = hotkey.KeyHome
	m["end"] = hotkey.KeyEnd
	m["pageup"] = hotkey.KeyPageUp
	m["pagedown"] = hotkey.KeyPageDown
	m["insert"] = hotkey.KeyInsert
	m["delete"] = hotkey.KeyDelete
	m["capslock"] = hotkey.KeyCapsLock
	m["`"] = hotkey.KeyBacktick
	m["-"] = hotkey.KeyMinus
	m["="] = hotkey.KeyEqual
	m["["] = hotkey.KeyLeftBracket
	m["]"] = hotkey.KeyRightBracket
	m["\\"] = hotkey.KeyBackslash
	m[";"] = hotkey.KeySemicolon
	m["'"] = hotkey.KeyQuote
	m[","] = hotkey.KeyComma
	m["."] = hotkey.KeyPeriod
	m["/"] = hotkey.KeySlash
	return m
}

// NormalizeMode returns the recognized mode name and an empty error, or
// ("", err) for unknown modes. Empty input defaults to "hold".
func NormalizeMode(mode string) (string, error) {
	switch mode {
	case "", "hold":
		return "hold", nil
	case "toggle":
		return "toggle", nil
	default:
		return "", fmt.Errorf("unknown voice mode %q (expected hold/toggle)", mode)
	}
}

// NormalizeUndoAction returns the recognized triple-tap retract action and an
// empty error, or ("", err) for unknown values. Empty input defaults to
// "undo".
func NormalizeUndoAction(action string) (string, error) {
	switch action {
	case "", "undo":
		return "undo", nil
	case "clear":
		return "clear", nil
	default:
		return "", fmt.Errorf("unknown undo_action %q (expected undo/clear)", action)
	}
}

func ParseHotkey(s string) (hotkey.Combo, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return hotkey.Combo{}, fmt.Errorf("empty hotkey string")
	}
	parts := strings.Split(s, "+")
	var mods hotkey.Modifier
	var key hotkey.KeyCode
	for _, part := range parts {
		part = strings.TrimSpace(part)
		lower := strings.ToLower(part)
		if mod, ok := modifierNames[lower]; ok {
			mods |= mod
			continue
		}
		if k, ok := keyNameToCode[lower]; ok {
			if key != hotkey.KeyNone {
				return hotkey.Combo{}, fmt.Errorf("multiple keys in %q", s)
			}
			if k.IsModifier() && key == hotkey.KeyNone {
				key = k
			} else if !k.IsModifier() {
				key = k
			}
			continue
		}
		return hotkey.Combo{}, fmt.Errorf("unknown key %q in %q", part, s)
	}
	if key == hotkey.KeyNone && mods != hotkey.ModNone {
		return hotkey.Combo{Mods: mods, Key: hotkey.KeyNone}, nil
	}
	if key != hotkey.KeyNone && mods == hotkey.ModNone && !key.IsModifier() {
		return hotkey.Combo{Mods: hotkey.ModNone, Key: key}, nil
	}
	if key.IsModifier() && mods == hotkey.ModNone {
		return hotkey.Combo{Mods: hotkey.KeyCodeToModifier(key), Key: hotkey.KeyNone}, nil
	}
	if key != hotkey.KeyNone && mods != hotkey.ModNone {
		return hotkey.Combo{Mods: mods, Key: key}, nil
	}
	return hotkey.Combo{}, fmt.Errorf("cannot parse hotkey %q", s)
}

func ParseHotkeys(strings []string) ([]hotkey.Combo, error) {
	var combos []hotkey.Combo
	for _, s := range strings {
		c, err := ParseHotkey(s)
		if err != nil {
			return nil, err
		}
		combos = append(combos, c)
	}
	return combos, nil
}
