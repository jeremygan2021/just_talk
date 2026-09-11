# Just Talk

[中文](README.md)

Just Talk is a desktop voice input tool. It records audio with a global hotkey, sends it to streaming ASR, and then copies the recognized text to the clipboard or submits it directly into the focused input field.

It is built for people who want to type less and speak more while coding, chatting, writing notes, or working with long text.

## Screenshot

![Just Talk TUI](docs/screenshot-tui.png)

## Features

- Global hotkey recording with `toggle` and `hold` modes:
  - `toggle`: tap once to start, tap again to stop.
  - `hold`: hold to record, release to stop (the recording status overlay shows `REC` while the key is held).
- Voice hotkeys are limited to keys suitable for global shortcuts: modifiers, function keys, Tab, CapsLock, arrow/navigation keys, and similar non-text keys. Letters, digits, punctuation, Space, and other text-producing keys are rejected.
- Doubao ASR with three selectable backends: `doubao_url` (recommended, file-URL/base64 upload with a single `x-api-key`), `online` (streaming WebSocket, needs an AppKey + AccessKey pair), and `offline` (local SenseVoice, no network).
- Clipboard copy and automatic text submission.
- Multi-tap gestures (on by default): while idle, quickly tap the voice hotkey twice to **retract the input** (Ctrl+Z by default, or Ctrl+A then Backspace when configured to clear), and three times to **send Enter**, so text can be undone or submitted without touching the keyboard. The overlay briefly shows a `↶` retract arrow or a `⏎` return arrow instead of the record dot. Disable either gesture with `double_tap_undo` / `triple_tap_send`, and tune the detection gap with `triple_tap_ms`. They never fire while a recording, ASR finish, or paste is in flight.
- Always-on-top recording status overlay for Wayland, X11, and macOS.
- TUI configuration for hotkeys, mode, auto-submit, stop delay, hotwords, LLM, and related settings. The hotkey field supports `c` to record a combo directly without typing the textual representation.
- ASR hotwords for project names, people names, English terms, and domain-specific vocabulary.
- Usage statistics for total sessions, total recognized characters, average speed, and recent speed.

## Platform Status

The current development focus is Linux and macOS desktop support:

| Platform | Status | Notes |
| --- | --- | --- |
| Linux Wayland | Supported | Works with Sway / wlroots; hotkeys use evdev and require input permissions |
| Linux X11 | Supported | Uses native X11 global hotkeys |
| macOS | Supported | Global hotkeys use CGEventTap, recording uses CoreAudio, clipboard uses NSPasteboard, and overlay uses AppKit NSPanel |
| Windows | Not implemented | Not supported yet |

## Build

Just Talk uses native platform APIs, so builds require cgo.

Linux build dependencies:

```bash
# Arch Linux
sudo pacman -S --needed go gcc libx11 libxtst libxext wayland

# Debian / Ubuntu
sudo apt install golang-go build-essential libx11-dev libxtst-dev libxext-dev libxinerama-dev libwayland-dev
```

macOS build dependencies:

```bash
# Apple Command Line Tools provide clang and the macOS SDK. Full Xcode is not required.
xcode-select --install
```

Build for the current platform:

```bash
cd just-talk-go
CGO_ENABLED=1 go build -o build/just-talk ./cmd/just-talk
```

Install to `~/.local/bin/just-talk`:

```bash
# Make sure ~/.local/bin is in PATH. If not, add this line to ~/.bashrc or ~/.zshrc.
# export PATH="$HOME/.local/bin:$PATH"
build/just-talk --install
# or
make install
```

macOS must be built on macOS. The project does not provide a non-cgo build.

## Usage

Start the TUI:

```bash
just-talk
```

Run without the TUI:

```bash
just-talk --no-tui
```

Force a backend:

```bash
just-talk --backend wayland
just-talk --backend x11
```

## Configuration

Default config path:

```text
~/.config/just-talk/config.toml
```

Recommended hotkey config:

```toml
[voice]
mode = "toggle"
push_to_talk = "Alt+Super"
```

`Alt+Super` with `toggle` mode is recommended. Press once to start recording, then press again to stop. This avoids hold-mode key conflicts with desktop environments or focused input fields.

Voice hotkeys only support keys suitable for global shortcuts:

- Supported: modifier-only combinations, such as `Alt+Super` and `Ctrl+Alt+Shift`.
- Supported: function keys `F1` through `F24`, such as `F9` and `Alt+F8`.
- Supported: non-text control and navigation keys, such as `Tab`, `Enter`, `Escape`, `Backspace`, `CapsLock`, `Up`, `Down`, `Left`, `Right`, `Home`, `End`, `PageUp`, `PageDown`, `Insert`, and `Delete`.
- Not supported: letters, digits, punctuation, Space, numpad digits, and numpad symbols that can enter text, such as `Alt+G`, `G`, `Alt+1`, and `Alt+Space`.

Hotword example:

```toml
[voice]
hotwords = ["Wayland", "Sway", "wl-copy", "wtype", "just-talk-go"]
```

Triple-tap Enter / double-tap retract example (enabled by default):

```toml
[voice]
# Quickly tap the hotkey three times while idle to press Enter
triple_tap_send = true
# Quickly tap the hotkey twice while idle to retract the pasted text
double_tap_undo = true
# Retract action: undo = Ctrl+Z; clear = Ctrl+A then Backspace
double_tap_action = "undo"
# Maximum gap between taps (ms); clamped below stop_delay_ms
triple_tap_ms = 500
```

`doubao_url` backend (recommended, single `volc.seedasr.auc` token):

```toml
[voice]
asr_backend = "doubao_url"
doubao_api_key = "e929586a-7b5f-4584-8392-5fe1bb79ddc7"
doubao_resource_id = "volc.seedasr.auc"
```

This backend buffers the whole recording, base64-encodes it as a WAV blob, submits it to the `auc` submit endpoint, then polls the `query` endpoint until the final transcript is ready. No interim transcripts are emitted, but stop-to-text latency is typically under three seconds.

macOS hotkey example:

```toml
[voice]
# Option is Alt; Command/Cmd is Super.
push_to_talk = "Option+Command"
```

## Changelog

See [CHANGELOG.md](CHANGELOG.md).

## Maintenance And Contributions

Just Talk is maintained by `whoamihappyhacking`.

This project does not accept pull requests. Issues are welcome for bug reports, usage feedback, and feature discussion.

## License

Just Talk is licensed under the GNU General Public License v3.0.
