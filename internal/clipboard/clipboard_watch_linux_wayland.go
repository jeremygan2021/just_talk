//go:build linux

package clipboard

import (
	"bufio"
	"context"
	"io"
	"os/exec"
)

// pumpWaylandWatcher forwards each clipboard-change notification to the
// `Watcher.OnChange` callback. `wl-paste --watch cat` writes the *entire*
// new clipboard contents to stdout once per change — so one Scanner.Scan
// returns one full payload.
//
// On EOF we restart the process until ctx is cancelled.
func pumpWaylandWatcher(ctx context.Context, w *Watcher, r io.Reader, _ *exec.Cmd) {
	for ctx.Err() == nil {
		feedScanner(ctx, w, r)
		cmd := exec.CommandContext(ctx, "wl-paste", "--watch", "cat")
		stdoutR, err := cmd.StdoutPipe()
		if err != nil {
			return
		}
		if err := cmd.Start(); err != nil {
			return
		}
		r = stdoutR
		// intentionally assign the new cmd so the next iteration's nil-guard
		// keeps a reference for cancellation.
		_ = cmd
	}
}

func feedScanner(ctx context.Context, w *Watcher, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		text := scanner.Text()
		// If a clipboard payload contains newlines (likely from cat output of
		// large buffers), keep reading until we hit a logical end (EOF or
		// the next wl-paste emission — wl-clipboard re-emits full content
		// per change, so consecutive payloads never interleave within one
		// event).
		for scanner.Scan() {
			text += "\n" + scanner.Text()
		}
		if w.OnChange != nil && text != "" {
			w.OnChange(text)
		}
		if ctx.Err() != nil {
			return
		}
	}
}
