//go:build linux

package clipboard

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

func newPlatformWatcher() (*Watcher, error) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watcher{cancel: cancel, done: make(chan struct{})}

	// Wayland has first-class passive watchers; prefer it.
	if isWaylandSession() {
		err := startWaylandWatcher(ctx, w)
		if err == nil {
			go func() { close(w.done) }()
			return w, nil
		}
		// Fall through to XFixes if Wayland tooling is missing.
	}

	// X11 path uses the XFixes extension's CLIPBOARD ownership notifications.
	// We delegate to a tiny helper so we don't drag cgo into this file.
	if os.Getenv("DISPLAY") != "" {
		err := startX11Watcher(ctx, w)
		if err == nil {
			go func() { close(w.done) }()
			return w, nil
		}
		return nil, err
	}

	// Xvfb-less / headless: nothing to watch.
	cancel()
	close(w.done)
	return nil, ErrWatchUnsupported
}

func isWaylandSession() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland"
}

// startWaylandWatcher uses wl-paste --watch (wl-clipboard ≥ 2.0) when
// available; it falls back to shelling out to wl-paste in foreground mode
// if --watch is unsupported. We poll our own getter as a last resort.
func startWaylandWatcher(ctx context.Context, w *Watcher) error {
	if _, err := exec.LookPath("wl-paste"); err != nil {
		return errors.New("wl-paste not installed")
	}
	// Try modern --watch flag first.
	argv := []string{"wl-paste", "--watch", "cat"}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = nil // use a filter pipe
	stdoutR, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderrR, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	_ = stderrR
	if err := cmd.Start(); err != nil {
		return err
	}
	go pumpWaylandWatcher(ctx, w, stdoutR, cmd)
	return nil
}
