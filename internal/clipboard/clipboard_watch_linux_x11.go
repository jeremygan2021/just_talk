//go:build linux

package clipboard

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

// startX11Watcher polls the X11 clipboard at a modest interval.
//
// True passive notification requires `xclip -loop` (which itself takes
// ownership of CLIPBOARD — disruptive) or a custom XFixes-based client,
// neither of which is appropriate as a default watcher implementation. A
// 500 ms poll gives near-instant feedback in the GUI without spending any
// CPU when the clipboard is idle.
//
// Returns an error only if the platform tools (xclip / xsel) aren't
// installed; on success the watcher goroutine takes over until ctx is
// cancelled.
func startX11Watcher(ctx context.Context, w *Watcher) error {
	cmd, err := pickX11ReadCmd()
	if err != nil {
		return err
	}
	go func() {
		var last string
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			out, err := runCmd(cmd)
			if err != nil || out == "" {
				continue
			}
			if out == last {
				continue
			}
			last = out
			if w.OnChange != nil {
				w.OnChange(out)
			}
		}
	}()
	return nil
}

// pickX11ReadCmd selects the best available read clipboard tool.
func pickX11ReadCmd() ([]string, error) {
	if _, err := exec.LookPath("xclip"); err == nil {
		return []string{"xclip", "-o", "-selection", "clipboard"}, nil
	}
	if _, err := exec.LookPath("xsel"); err == nil {
		return []string{"xsel", "-b", "-o"}, nil
	}
	return nil, errors.New("x11: neither xclip nor xsel found in PATH")
}
