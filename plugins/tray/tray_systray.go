//go:build linux || darwin

package tray

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"fyne.io/systray"
	"github.com/c/just-talk-go/engine"
	"github.com/c/just-talk-go/plugins/voice"
)

// runSystray is the platform implementation hook. It starts fyne.io/systray
// in its own goroutine and returns a stop function. When the supplied ctx
// is cancelled, the caller invokes stop to release the tray icon.
//
// runSystray returns an error only for fatal initialisation failures so
// that the engine continues running with the tray disabled.
var runSystray = func(ctx context.Context, eng *engine.Engine, logger *slog.Logger) (func(), error) {
	onExit := make(chan struct{})
	ready := make(chan struct{})

	go systray.Run(func() {
		initMenu(eng, logger)
		close(ready)
	}, func() {
		close(onExit)
	})

	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		return nil, fmt.Errorf("tray did not become ready within 3s")
	}

	stop := func() {
		systray.Quit()
		select {
		case <-onExit:
		case <-time.After(2 * time.Second):
			logger.Warn("tray did not exit within 2s")
		}
	}

	go watchState(ctx, logger)
	go watchExit(ctx, eng, stop, onExit)

	return stop, nil
}

func initMenu(eng *engine.Engine, logger *slog.Logger) {
	mShow := systray.AddMenuItem("Show TUI", "Open the configuration UI in a new terminal")
	systray.AddSeparator()
	mCfg := systray.AddMenuItem("Open Config", "Open the configuration file")
	mDir := systray.AddMenuItem("Open Config Dir", "Open the configuration directory")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Stop the Just Talk daemon")

	if idle, err := iconForState("idle"); err == nil {
		systray.SetIcon(idle)
	}
	systray.SetTooltip("Just Talk — idle")

	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				if err := spawnTUI(); err != nil {
					logger.Warn("failed to spawn TUI", "error", err)
				}
			case <-mCfg.ClickedCh:
				if err := openConfigFile(); err != nil {
					logger.Warn("failed to open config", "error", err)
				}
			case <-mDir.ClickedCh:
				if err := openConfigDir(); err != nil {
					logger.Warn("failed to open config dir", "error", err)
				}
			case <-mQuit.ClickedCh:
				logger.Info("quit requested from tray")
				systray.Quit()
				if eng != nil {
					eng.Stop()
				}
				return
			}
		}
	}()
}

func watchState(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastState string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st := voice.TUIStatus().State
			if st == lastState {
				continue
			}
			lastState = st
			icon, err := iconForState(st)
			if err != nil {
				logger.Debug("tray icon render failed", "error", err)
				continue
			}
			systray.SetIcon(icon)
			systray.SetTooltip(tooltipForState(st))
		}
	}
}

func watchExit(ctx context.Context, eng *engine.Engine, stop func(), onExit chan struct{}) {
	select {
	case <-ctx.Done():
		stop()
	case <-onExit:
		if eng != nil {
			eng.Stop()
		}
	}
}

// spawnTUI starts a new terminal window that runs `just-talk` (TUI mode).
//
// The spawned process is independent from the daemon — closing the
// terminal only ends the TUI session.
func spawnTUI() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	cmd := buildTerminalCommand(exe)
	if cmd == nil {
		return fmt.Errorf("no supported terminal emulator found")
	}
	return cmd.Start()
}

func buildTerminalCommand(exe string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("osascript", "-e",
			fmt.Sprintf(`tell application "Terminal" to do script %q`, quoteForOSA(exe)))
	}

	candidates := [][]string{
		{"x-terminal-emulator", "-e", exe},
		{"gnome-terminal", "--", exe},
		{"konsole", "-e", exe},
		{"xfce4-terminal", "-e", exe},
		{"mate-terminal", "-e", exe},
		{"tilix", "-e", exe},
		{"alacritty", "-e", exe},
		{"kitty", exe},
	}
	for _, parts := range candidates {
		if _, err := exec.LookPath(parts[0]); err == nil {
			return exec.Command(parts[0], parts[1:]...)
		}
	}
	return nil
}

func quoteForOSA(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		if r == '"' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, byte(r))
	}
	out = append(out, '"')
	return string(out)
}

func openConfigFile() error {
	path := findConfigPath()
	if path == "" {
		return fmt.Errorf("config file not found")
	}
	return openWithDefault(path)
}

func openConfigDir() error {
	dir := findConfigDir()
	return openWithDefault(dir)
}

func findConfigPath() string {
	for _, candidate := range configCandidates() {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func findConfigDir() string {
	for _, candidate := range configCandidates() {
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Dir(candidate)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "just-talk")
}

func configCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{"./config.toml"}
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	out := []string{"./config.toml"}
	if xdg != "" {
		out = append(out, filepath.Join(xdg, "just-talk", "config.toml"))
	}
	out = append(out, filepath.Join(home, ".config", "just-talk", "config.toml"))
	return out
}

func openWithDefault(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "linux":
		cmd = exec.Command("xdg-open", target)
	default:
		return fmt.Errorf("opening files is not supported on %s", runtime.GOOS)
	}
	return cmd.Start()
}
