//go:build !linux && !darwin

package clipboard

import (
	"context"
)

func newPlatformWatcher() (*Watcher, error) {
	return nil, ErrWatchUnsupported
}

var _ = context.Background
