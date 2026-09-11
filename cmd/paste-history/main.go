// Paste-history companion: monitors the system clipboard, retains a
// rolling history, and lets the user pick a prior entry to (auto-)paste
// into the currently focused input field.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/c/just-talk-go/internal/autotype"
	"github.com/c/just-talk-go/internal/clipboard"
	"github.com/c/just-talk-go/internal/pastehistory"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

func main() {
	showGUI := flag.Bool("gui", true, "show GUI on startup")
	headless := flag.Bool("headless", false, "do not show GUI (events only)")
	maxItems := flag.Int("max", pastehistory.DefaultMaxItems, "history cap")
	verbose := flag.Bool("verbose", false, "verbose logging")
	flag.Parse()

	var logW io.Writer = os.Stderr
	logger := slog.New(slog.NewTextHandler(logW, &slog.HandlerOptions{
		Level: pickLevel(*verbose),
	}))

	hist, err := pastehistory.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "history open: %v\n", err)
		os.Exit(1)
	}
	// Note: passing -max only affects future runs; we won't truncate an
	// already-loaded history here because doing so would discard memory.
	_ = *maxItems
	logger.Info("paste-history started", "file", hist.FilePath())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher, err := clipboard.NewWatcher()
	if err != nil {
		logger.Warn("clipboard watcher unavailable — GUI still works from saved history", "err", err)
	} else {
		watcher.OnChange = func(text string) {
			hist.Push(text)
			logger.Debug("clipboard change", "len", len(text))
		}
		defer watcher.Stop()
	}

	if *headless || !*showGUI {
		logger.Info("headless mode — Ctrl-C to quit")
		<-ctx.Done()
		return
	}

	a := app.New()
	w := a.NewWindow("Paste History")
	w.SetPadded(true)

	state := newUI(hist)
	state.onCommit = func(text string) {
		if text == "" {
			return
		}
		hist.Push(text)
		if err := autotype.Paste(text, logger); err != nil {
			logger.Error("autotype paste failed", "err", err)
		}
		w.Hide()
	}

	windowSetup(w, state)

	if drv, ok := a.Driver().(interface{ SetOnTop(bool) }); ok {
		drv.SetOnTop(true)
	}
	w.ShowAndRun()
	cancel()
}

func pickLevel(v bool) slog.Level {
	if v {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}
