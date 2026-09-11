//go:build windows

package autotype

import (
	"fmt"
	"log/slog"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32        = windows.NewLazySystemDLL("user32.dll")
	procSendInput = user32.NewProc("SendInput")
	procVkKeyScan = user32.NewProc("VkKeyScanW")
)

type keyboardInput struct {
	Type    uint32
	Wvk     uint16
	Wscan   uint16
	DwFlags uint32
	Time    uint32
	DwExtra uintptr
}

type input struct {
	Type uint32
	Ki   keyboardInput
}

const (
	inputKeyboard  = 1
	keyeventfKeyUp = 2
)

func pastePlatform(text string, logger *slog.Logger) error {
	return fmt.Errorf("autotype on Windows is not implemented")
}

func simulatePaste() error {
	// Simulate Ctrl down → V down → V up → Ctrl up
	keys := []struct {
		code uint16
		up   bool
	}{
		{0x11, false}, // VK_CONTROL down
		{0x56, false}, // VK_V down
		{0x56, true},  // VK_V up
		{0x11, true},  // VK_CONTROL up
	}

	var inputs []input
	for _, k := range keys {
		flags := uint32(0)
		if k.up {
			flags = keyeventfKeyUp
		}
		inputs = append(inputs, input{
			Type: inputKeyboard,
			Ki: keyboardInput{
				Wvk:     k.code,
				DwFlags: flags,
			},
		})
	}

	cbSize := unsafe.Sizeof(input{})
	procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		uintptr(cbSize),
	)
	return nil
}

func pasteMethod() string { return "windows/SendInput+Ctrl+V" }

// sendEnterPlatform simulates a Return key press via SendInput.
func sendEnterPlatform(logger *slog.Logger) error {
	sendWinKey(0x0D) // VK_RETURN
	logger.Debug("send enter done", "method", "windows/SendInput+Return")
	return nil
}

func sendUndoPlatform(logger *slog.Logger) error {
	sendWinCtrlKey(0x5A) // VK_Z
	logger.Debug("undo done", "method", "windows/SendInput+Ctrl+Z")
	return nil
}

func sendClearInputPlatform(logger *slog.Logger) error {
	sendWinCtrlKey(0x41) // VK_A
	time.Sleep(30 * time.Millisecond)
	sendWinKey(0x08) // VK_BACK
	logger.Debug("clear input done", "method", "windows/SendInput+Ctrl+A+Backspace")
	return nil
}

func sendWinKey(vk uint16) {
	inputs := []input{
		{Type: inputKeyboard, Ki: keyboardInput{Wvk: vk}},
		{Type: inputKeyboard, Ki: keyboardInput{Wvk: vk, DwFlags: keyeventfKeyUp}},
	}
	sendWinInputs(inputs)
}

func sendWinCtrlKey(vk uint16) {
	inputs := []input{
		{Type: inputKeyboard, Ki: keyboardInput{Wvk: 0x11}}, // VK_CONTROL down
		{Type: inputKeyboard, Ki: keyboardInput{Wvk: vk}},
		{Type: inputKeyboard, Ki: keyboardInput{Wvk: vk, DwFlags: keyeventfKeyUp}},
		{Type: inputKeyboard, Ki: keyboardInput{Wvk: 0x11, DwFlags: keyeventfKeyUp}},
	}
	sendWinInputs(inputs)
}

func sendWinInputs(inputs []input) {
	procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		uintptr(unsafe.Sizeof(input{})),
	)
}

func isWaylandSession() bool { return false }
