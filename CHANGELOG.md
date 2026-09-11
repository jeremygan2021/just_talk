# Changelog

All notable project changes are tracked here.

## Unreleased

- Added a Linux/macOS system tray indicator (`plugins/tray`) that shows a colored status icon (grey idle, yellow connecting, red recording, orange stopping, dark red error, green/orange gesture arrows) and exposes Show TUI, Open Config, Open Config Dir, and Quit menu actions. The plugin runs only in daemon mode (`--no-tui`) and registers as a `org.kde.StatusNotifierItem` on DBus, which the GNOME `AppIndicator` extension and KDE Plasma render natively.
- Added `make icons` / `make install-desktop` / `make uninstall-desktop` to install the desktop launcher icon and `.desktop` file into `~/.local/share/applications/` and `~/Desktop/`. The `.desktop` entry launches the binary in daemon mode, so clicking the icon or selecting it from the application menu starts Just Talk with the tray icon.
- Added a `flock`-based single-instance lock (`$XDG_RUNTIME_DIR/just-talk/just-talk.lock`). A second launch is rejected with a clear log message instead of contending for global hotkeys; stale locks from a crashed previous instance are automatically reclaimed.
- Removed the `~/.config/systemd/user/just-talk.service` autostart unit. Just Talk is now meant to be launched on demand from the desktop; users who want autostart can add their own systemd `user` unit or place a startup script in `~/.config/autostart/`.
- Wayland/evdev backend now listens to `NETLINK_KOBJECT_UEVENT` for input device hotplug. Keyboards (including Bluetooth HID devices that pair after startup, USB devices plugged in after launch, etc.) are picked up immediately as `/dev/input/eventN` nodes appear, without requiring a restart of just-talk. Device removals are also handled so unplugged devices do not stay in the poll set.
- Added multi-tap hotkey gestures for chat/coding workflows. While idle, quickly tapping the voice hotkey twice presses Enter in the focused window (the most frequent action, so it gets the easier gesture), and tapping it three times retracts the just-pasted text (`Ctrl+Z` by default, or `Ctrl+A` then `Backspace` with `undo_action = "clear"`). A gesture that starts while a recording, ASR finish, or paste is already in flight is ignored. The overlay briefly shows a `⏎` return arrow or a `↶` retract arrow instead of the recording indicator; the return glyph was redrawn so it no longer reads as a `J`. Controlled by `voice.double_tap_send`, `voice.triple_tap_undo`, `voice.undo_action`, and `voice.multi_tap_ms` (the shared multi-tap window, default 500 ms, clamped below `stop_delay_ms`).
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
