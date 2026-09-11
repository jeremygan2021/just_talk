//go:build !(linux || darwin)

package tray

import (
	"context"
	"log/slog"

	"github.com/c/just-talk-go/engine"
)

// runSystray is the platform implementation hook.
//
// On unsupported platforms (e.g. Windows) it is nil, which causes
// Plugin.Start to log and return immediately without crashing the engine.
var runSystray func(ctx context.Context, eng *engine.Engine, logger *slog.Logger) (func(), error) = nil
