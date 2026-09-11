// Package tray provides a system tray indicator for the daemon mode.
//
// On Linux and macOS the plugin embeds fyne.io/systray and renders a
// colored status icon (idle / recording / stopping / error) together with
// a context menu that exposes common actions. On unsupported platforms
// the plugin compiles to a no-op.
package tray

import (
	"context"
	"log/slog"

	"github.com/c/just-talk-go/engine"
	"github.com/c/just-talk-go/plugins/voice"
)

// Plugin is the system tray indicator plugin.
//
// It listens to voice.TUIStatus() and updates the tray icon whenever the
// voice state changes. The tray menu offers:
//
//   - Show TUI  — spawn a new terminal running the TUI configuration UI.
//   - Open Config — open the configuration file with the default app.
//   - Open Config Dir — open the configuration directory in the file manager.
//   - Quit — gracefully shut down the daemon.
//
// Register this plugin only in daemon mode. The TUI mode already owns the
// terminal and would conflict with the tray icon.
type Plugin struct {
	logger   *slog.Logger
	engine   *engine.Engine
	stopFunc func()
}

// NewTrayPlugin creates a tray plugin instance.
func NewTrayPlugin() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string    { return "tray" }
func (p *Plugin) Version() string { return "0.1.0" }

// Init captures the plugin environment. Hotkey registration is not used by
// this plugin, so the work here is minimal — we only stash the logger and
// a handle to the engine so that Quit can shut the daemon down cleanly.
func (p *Plugin) Init(env engine.PluginEnv) error {
	p.logger = env.Logger()
	p.engine = env.Engine()
	return nil
}

// Start runs the tray icon until the engine context is cancelled.
//
// runSystray is provided per platform. On unsupported platforms it is nil
// and Start returns immediately so the engine keeps working.
func (p *Plugin) Start(ctx context.Context) error {
	if runSystray == nil {
		p.logger.Info("tray not supported on this platform")
		return nil
	}
	stop, err := runSystray(ctx, p.engine, p.logger)
	if err != nil {
		p.logger.Warn("tray failed to start", "error", err)
		return nil
	}
	p.stopFunc = stop
	<-ctx.Done()
	if p.stopFunc != nil {
		p.stopFunc()
		p.stopFunc = nil
	}
	return ctx.Err()
}

// Stop tears down the tray icon (if still running) when the engine shuts down.
func (p *Plugin) Stop() error {
	if p.stopFunc != nil {
		p.stopFunc()
		p.stopFunc = nil
	}
	return nil
}

// iconForState resolves a tray icon for the given voice state string.
func iconForState(state string) ([]byte, error) {
	return iconFor(state)
}

// tooltipForState returns a human-readable label for the given voice state.
func tooltipForState(state string) string {
	switch state {
	case "connecting":
		return "Just Talk — connecting to ASR"
	case "recording":
		return "Just Talk — recording"
	case "stopping":
		return "Just Talk — finalizing"
	case "stopping_delayed":
		return "Just Talk — waiting to stop"
	case "error":
		return "Just Talk — error"
	case "enter":
		return "Just Talk — sent Enter"
	case "undo":
		return "Just Talk — undo sent"
	default:
		status := voice.TUIStatus()
		if status.Detail != "" {
			return "Just Talk — " + status.Detail
		}
		return "Just Talk — idle"
	}
}
