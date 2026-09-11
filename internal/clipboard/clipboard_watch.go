// Package clipboard — watcher side.
package clipboard

import (
	"context"
	"errors"
)

// Watcher emits clipboard text changes on OnChange.
//
// Implementations are platform-specific. Cancel the context to stop the
// watcher.
type Watcher struct {
	OnChange func(text string)
	cancel   context.CancelFunc
	done     chan struct{}
}

// ErrWatchUnsupported is returned by NewWatcher on platforms / sessions
// that have no reliable passive listener (e.g. headless).
var ErrWatchUnsupported = errors.New("clipboard watch not supported on this platform/session")

// NewWatcher starts listening for clipboard content changes.
//
// The watcher delivers the new content via w.OnChange; identical consecutive
// values are not reported twice (the underlying transport already provides
// change-notification semantics).
//
// Call w.Stop() to detach the watcher.
func NewWatcher() (*Watcher, error) {
	return newPlatformWatcher()
}

// Stop detaches the watcher and blocks until the underlying goroutine
// returns. Calling Stop more than once is a no-op.
func (w *Watcher) Stop() {
	if w == nil || w.cancel == nil {
		return
	}
	w.cancel()
	w.cancel = nil
	<-w.done
	w.done = nil
}
