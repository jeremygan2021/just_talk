# Changelog

All notable project changes are tracked here.

## Unreleased

- Added a triple-tap Enter gesture for chat/coding workflows: while idle, quickly tapping the voice hotkey three times presses Enter in the focused window so already-pasted text can be sent without touching the keyboard. A gesture that starts while a recording is already in flight is ignored. The overlay briefly shows a `⏎` Return-key hint instead of the recording indicator. Controlled by `voice.triple_tap_send` (default on) and `voice.triple_tap_ms` (default 500 ms; clamped to `stop_delay_ms`).
- Added a new `doubao_url` ASR backend that uses the Doubao/Volcano `auc` submit+query HTTP endpoint with a single `x-api-key`. This is the official replacement for the streaming WebSocket `sauc` AppKey/AccessKey credentials and works with the seedasr.auc token pair. The backend buffers PCM, wraps the recording in a WAV container, base64-encodes it, submits it, then polls the query endpoint until the final transcript is returned. No partial results are emitted, but latency from stop to text is typically under three seconds.
- Removed the `press` hotkey mode and its associated LLM polish pipeline. The voice plugin now supports only `hold` (press and hold to record, release to stop) and `toggle` (press once to start, press again to stop). The recording status overlay continues to display the `REC` indicator while a recording is in flight, matching the pre-`press` behavior.
- Added an optional OpenAI-compatible LLM polish step (`[llm]` in config). When `mode = "press"` and the recording was started via a short press, the ASR result is routed through the LLM before being pasted. LLM failures fall back to pasting the raw ASR text and surface a one-line warning.
- Added a hotkey capture mode in the TUI: pressing `c` while editing the 热键 field listens for the next global key combo and stores it as the new hotkey, replacing manual text input.
- Clarified README build and install setup steps for the repository directory and `~/.local/bin` PATH.
- Restricted voice hotkeys to non-text global shortcut keys, rejecting letters, digits, punctuation, Space, and similar text-producing keys.
- Avoid duplicate auto-submit on KDE Plasma by using uinput directly and not writing the Wayland primary selection there.
- TUI is now the default startup mode.
- Added persistent usage statistics for total sessions, recognized characters, average speed, and recent speed.
- Added configurable ASR hotwords.
- Added TUI help toggle with `h`.
- Improved Wayland clipboard and auto-submit behavior with `wl-copy` and `wtype`.
- Added Linux recording status overlay for X11 and Wayland.
- Added macOS support for global hotkeys, native recording, clipboard, auto-submit, recording status overlay, and environment checks.
- Removed non-cgo macOS fallback builds; Just Talk now requires cgo for native platform integration.
- Replaced the old Claude-specific agent guide with `AGENTS.md` and clarified build documentation.
- Improved toggle and hold hotkey behavior for fast repeated key presses.
- Show ASR connection and final-result timeout errors in the status UI/overlay instead of immediately falling back to idle.
- Added transient `Esc` cancel and `R` retry hotkeys while recording or showing retryable errors.
- Improved X11 overlay placement on multi-monitor setups and switched X11 rendering to an ARGB window for smoother rounded corners.
- Fixed a Wayland overlay shutdown race that could crash while closing the app, and surfaced Linux `arecord` microphone/device failures in the UI.
- Made `Esc` cancel active overlay states, including the final ASR wait state, and suppress output from canceled pending sessions.
- Improved Wayland overlay rounded-corner antialiasing, especially on KDE Plasma.
- Added `just-talk --install` and `make install` to install the binary into `~/.local/bin`.

## 2026-05-30

- Initial Linux-focused development snapshot.
- Supported Linux Wayland hotkeys via evdev.
- Supported Linux X11 hotkeys via native X11 grabs.
- Added Doubao streaming ASR integration.
- Added TUI configuration interface.
- Added automatic clipboard copy and auto-submit.
