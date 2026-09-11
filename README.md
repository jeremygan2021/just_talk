# Just Talk

[English](README.en.md)

减少用键盘的次数，改用口喷吧。

Just Talk 是一个面向桌面环境的语音输入工具。它通过全局快捷键录音，把语音识别结果复制到剪贴板，或直接上屏到当前输入框，适合写代码、聊天、记笔记和处理长文本输入。

## 截图

![Just Talk TUI](docs/screenshot-tui.png)

## 功能

- 全局快捷键录音，支持 `toggle` 和 `hold` 两种模式：
  - `toggle`：按一次开始，再按一次停止。
  - `hold`：按住录音，松手停止（屏幕 overlay 显示 `REC` 提示符）。
- 语音热键限定为适合作为全局快捷键的按键：支持纯修饰键、功能键、Tab、CapsLock、方向键和导航键等；不支持字母、数字、标点、空格等普通字符键。
- 豆包大模型 ASR，支持三种后端：`doubao_url`（推荐，文件 URL/Base64 上传，单 token `x-api-key`）、`online`（流式 WebSocket，需 AppKey+AccessKey）、`offline`（本地 SenseVoice，无需联网）。
- 自动复制到剪贴板，支持自动上屏。
- 多击手势（默认开启）：空闲时快速按两次语音热键 = **发送回车**（Enter，最常用所以放在更容易的双击上），快速按三次 = **撤回输入**（默认 `Ctrl+Z` 撤销刚上屏的文字，可配置为 `Ctrl+A`+`Backspace` 清空），方便在聊天、写代码时“说完即发送 / 说完即撤”。手势触发时 overlay 会短暂显示 `⏎` 回车箭头 / `↶` 撤回箭头替代录音圆点。可分别用 `double_tap_send`、`triple_tap_undo` 关闭；`multi_tap_ms` 调整判定间隔。正在录音、等待识别或正在上屏时不会触发。
- Wayland / X11 / macOS 顶层录音状态胶囊提示。
- TUI 配置界面，支持热键、模式、自动上屏、停止延迟、热词、LLM 等配置；热键字段支持 `c` 直接录制组合键，无需手动输入。
- 热词增强识别，适合项目名、人名、英文术语和专有名词。
- 录音历史统计，包括历史次数、总字数、平均速度和最近速度。

## 平台状态

当前开发重点是 Linux 和 macOS 桌面：

| 平台 | 状态 | 说明 |
| --- | --- | --- |
| Linux Wayland | 已支持 | 已支持 Sway / wlroots 场景；快捷键基于 evdev，需要 input 权限 |
| Linux X11 | 已支持 | 使用 X11 原生全局热键 |
| macOS | 已支持 | 全局快捷键基于 CGEventTap，录音使用 CoreAudio，剪贴板使用 NSPasteboard，胶囊显示使用 AppKit NSPanel |
| Windows | 未实现 | 暂不支持 |

## 构建

Just Talk 依赖平台原生能力，构建时需要启用 cgo。

Linux 构建依赖：

```bash
# Arch Linux
sudo pacman -S --needed go gcc libx11 libxtst libxext wayland

# Debian / Ubuntu
sudo apt install golang-go build-essential libx11-dev libxtst-dev libxext-dev libxinerama-dev libwayland-dev
```

macOS 构建依赖：

```bash
# 需要 Apple Command Line Tools 提供 clang 和 macOS SDK；不需要安装完整 Xcode。
xcode-select --install
```

构建当前平台二进制：

```bash
cd just-talk-go
CGO_ENABLED=1 go build -o build/just-talk ./cmd/just-talk
```

安装到 `~/.local/bin/just-talk`：

```bash
# 确保 ~/.local/bin 在 PATH 中（如未配置，将下面这行加入 ~/.bashrc 或 ~/.zshrc）
# export PATH="$HOME/.local/bin:$PATH"
build/just-talk --install
# 或
make install
```

macOS 需要在本机 macOS 上构建；项目不提供非 cgo 版本。

## 使用

默认启动 TUI：

```bash
just-talk
```

后台模式：

```bash
just-talk --no-tui
```

指定后端：

```bash
just-talk --backend wayland
just-talk --backend x11
```

## 配置

默认配置路径：

```text
~/.config/just-talk/config.toml
```

推荐热键配置：

```toml
[voice]
mode = "toggle"
push_to_talk = "Alt+Super"
```

`Alt+Super` 配合 `toggle` 模式是推荐用法。按一次开始录音，再按一次停止录音，避免按住模式下和桌面环境或输入框发生按键冲突。

语音热键只支持适合作为全局快捷键的按键：

- 支持：纯修饰键组合，如 `Alt+Super`、`Ctrl+Alt+Shift`。
- 支持：功能键 `F1` 到 `F24`，如 `F9`、`Alt+F8`。
- 支持：非文本控制键和导航键，如 `Tab`、`Enter`、`Escape`、`Backspace`、`CapsLock`、`Up`、`Down`、`Left`、`Right`、`Home`、`End`、`PageUp`、`PageDown`、`Insert`、`Delete`。
- 不支持：字母、数字、标点、空格、数字小键盘数字和符号等会输入文本的按键，如 `Alt+G`、`G`、`Alt+1`、`Alt+Space`。

热词示例：

```toml
[voice]
hotwords = ["Wayland", "Sway", "wl-copy", "wtype", "just-talk-go"]
```

双击回车 / 三击撤回示例（默认开启）：

```toml
[voice]
# 空闲时快速按两次热键 → 发送回车（最常用）
double_tap_send = true
# 空闲时快速按三次热键 → 撤回上屏内容
triple_tap_undo = true
# 撤回动作: undo = Ctrl+Z 撤销; clear = Ctrl+A 后 Backspace 清空
undo_action = "undo"
# 双击/三击判定最大按键间隔（毫秒），会自动收敛到小于 stop_delay_ms
multi_tap_ms = 500
```

`doubao_url` 后端配置（推荐，使用 `volc.seedasr.auc` 单 token）：

```toml
[voice]
asr_backend = "doubao_url"
doubao_api_key = "e929586a-7b5f-4584-8392-5fe1bb79ddc7"
doubao_resource_id = "volc.seedasr.auc"
```

此模式下整段录音结束后才会提交识别并轮询结果，不会输出中间识别；端到端延迟通常在 3 秒以内。

macOS 热键写法：

```toml
[voice]
# Option 等价于 Alt，Command/Cmd 等价于 Super
push_to_talk = "Option+Command"
```

## 更新日志

见 [CHANGELOG.md](CHANGELOG.md)。

## 维护与贡献

Just Talk 由 `whoamihappyhacking` 维护。

本项目不接受 Pull Request。欢迎通过 Issue 反馈 bug、使用体验和功能建议。

## 许可证

Just Talk 使用 GNU General Public License v3.0 开源。
# just_talk
