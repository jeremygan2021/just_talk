package autotype

import (
	"log/slog"
)

// Paste inserts text into the currently focused input field.
func Paste(text string, logger *slog.Logger) error {
	return pastePlatform(text, logger)
}

// SendEnter simulates a single Enter/Return key press in the focused window.
// It is used by the voice plugin's triple-tap gesture to submit already
// pasted text without touching the physical keyboard.
func SendEnter(logger *slog.Logger) error {
	return sendEnterPlatform(logger)
}

// SendUndo simulates Ctrl+Z in the focused window, undoing the most recent
// edit (typically the auto-submitted paste).
func SendUndo(logger *slog.Logger) error {
	return sendUndoPlatform(logger)
}

// SendClearInput simulates Ctrl+A followed by Backspace, clearing the whole
// focused input field.
func SendClearInput(logger *slog.Logger) error {
	return sendClearInputPlatform(logger)
}
