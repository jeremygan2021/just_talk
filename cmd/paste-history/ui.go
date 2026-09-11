package main

import (
	"fmt"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/c/just-talk-go/internal/pastehistory"
)

// uiState is the GUI's transient state — what the user is currently
// looking at — plus the callbacks into the daemon.
type uiState struct {
	hist       *pastehistory.History
	selected   int
	onCommit   func(text string)
	list       *widget.List
	status     *widget.Label
	lastLength int32 // atomic, for cheap polling
	stop       chan struct{}
}

// newUI wires up the GUI state but does NOT attach it to a window yet —
// windowSetup does that after we know the fyne.App handles.
func newUI(hist *pastehistory.History) *uiState {
	return &uiState{hist: hist, stop: make(chan struct{})}
}

// windowSetup installs the popup widgets, registers key handlers, and
// applies the visual treatment (no window border, narrower than default).
func windowSetup(w fyne.Window, s *uiState) {
	s.list = widget.NewList(
		func() int { return len(s.hist.Items()) },
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(i int, o fyne.CanvasObject) {
			items := s.hist.Items()
			if i >= len(items) {
				return
			}
			entry := items[i]
			label := o.(*widget.Label)
			label.SetText(fmt.Sprintf(" %2d  %s", i+1, entry.Preview))
		},
	)
	s.list.OnSelected = func(id int) {
		s.selected = id
		s.commitSelected()
	}

	s.status = widget.NewLabel("Enter / click → paste · Esc → hide · ↑/↓ to navigate · 1-9 to pick")

	header := widget.NewLabelWithStyle("Recent clipboard", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	body := container.NewBorder(
		header,
		s.status,
		nil, nil, s.list,
	)
	w.SetContent(body)
	w.Resize(fyne.NewSize(560, 420))

	w.Canvas().SetOnTypedKey(func(e *fyne.KeyEvent) {
		switch e.Name {
		case fyne.KeyEscape:
			w.Hide()
		case fyne.KeyReturn, fyne.KeyEnter:
			s.commitSelected()
		case fyne.KeyUp:
			s.nudge(-1)
		case fyne.KeyDown:
			s.nudge(1)
		}
		if idx, ok := numericName(e.Name); ok {
			if idx < len(s.hist.Items()) {
				s.selected = idx
				s.commitSelected()
			}
		}
	})

	// Refresh whenever items change — cheapest is to poll.
	go s.refreshLoop()
}

// refreshLoop re-reads the history and pokes fyne to redraw the list.
func (s *uiState) refreshLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
		}
		items := s.hist.Items()
		newLen := int32(len(items))
		if atomic.LoadInt32(&s.lastLength) != newLen {
			atomic.StoreInt32(&s.lastLength, newLen)
			if s.list != nil {
				fyne.Do(func() { s.list.Refresh() })
			}
		}
	}
}

func (s *uiState) nudge(delta int) {
	items := s.hist.Items()
	if len(items) == 0 {
		return
	}
	s.selected += delta
	if s.selected < 0 {
		s.selected = 0
	}
	if s.selected >= len(items) {
		s.selected = len(items) - 1
	}
	s.list.Select(s.selected)
}

func (s *uiState) commitSelected() {
	items := s.hist.Items()
	if s.selected >= len(items) || s.selected < 0 {
		return
	}
	if s.onCommit != nil {
		s.onCommit(items[s.selected].Text)
	}
}

// numericName returns the 0-based index when `name` is a fyne digit key.
func numericName(name fyne.KeyName) (int, bool) {
	switch name {
	case fyne.Key1:
		return 0, true
	case fyne.Key2:
		return 1, true
	case fyne.Key3:
		return 2, true
	case fyne.Key4:
		return 3, true
	case fyne.Key5:
		return 4, true
	case fyne.Key6:
		return 5, true
	case fyne.Key7:
		return 6, true
	case fyne.Key8:
		return 7, true
	case fyne.Key9:
		return 8, true
	}
	return -1, false
}
