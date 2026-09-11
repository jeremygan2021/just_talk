//go:build darwin

package clipboard

import (
	"context"
)

// On macOS we use Objective-C NSPasteboard.changeCount polling because
// a real passive observer requires CFRunLoop + AppKit. Polling is good
// enough at ~250 ms.
func newPlatformWatcher() (*Watcher, error) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watcher{cancel: cancel, done: make(chan struct{})}
	if err := startDarwinWatcher(ctx, w); err != nil {
		cancel()
		close(w.done)
		return nil, err
	}
	go func() { close(w.done) }()
	return w, nil
}
