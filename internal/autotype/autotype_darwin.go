//go:build darwin && cgo

package autotype

// #cgo LDFLAGS: -framework ApplicationServices
//
// #include <ApplicationServices/ApplicationServices.h>
// #include <unistd.h>
//
// static void cgevent_cmd_v(void) {
// 	CGEventRef cmdDown = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)55, true);  // kVK_Command
// 	CGEventRef vDown   = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)9, true);   // kVK_ANSI_V
// 	CGEventRef vUp     = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)9, false);
// 	CGEventRef cmdUp   = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)55, false);
//
// 	CGEventSetFlags(vDown, kCGEventFlagMaskCommand);
// 	CGEventSetFlags(vUp, kCGEventFlagMaskCommand);
//
// 	CGEventPost(kCGSessionEventTap, cmdDown);
// 	usleep(15000);
// 	CGEventPost(kCGSessionEventTap, vDown);
// 	usleep(30000);
// 	CGEventPost(kCGSessionEventTap, vUp);
// 	usleep(15000);
// 	CGEventPost(kCGSessionEventTap, cmdUp);
//
// 	CFRelease(cmdDown); CFRelease(vDown); CFRelease(vUp); CFRelease(cmdUp);
// }
//
// static void cgevent_return(void) {
// 	CGEventRef retDown = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)36, true);  // kVK_Return
// 	CGEventRef retUp   = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)36, false);
//
// 	CGEventPost(kCGSessionEventTap, retDown);
// 	usleep(20000);
// 	CGEventPost(kCGSessionEventTap, retUp);
//
// 	CFRelease(retDown); CFRelease(retUp);
// }
//
// static void cgevent_ctrl_key(CGKeyCode code) {
// 	CGEventRef down = CGEventCreateKeyboardEvent(NULL, code, true);
// 	CGEventRef up   = CGEventCreateKeyboardEvent(NULL, code, false);
//
// 	CGEventSetFlags(down, kCGEventFlagMaskControl);
// 	CGEventSetFlags(up, kCGEventFlagMaskControl);
//
// 	CGEventPost(kCGSessionEventTap, down);
// 	usleep(30000);
// 	CGEventPost(kCGSessionEventTap, up);
//
// 	CFRelease(down); CFRelease(up);
// }
//
// static void cgevent_ctrl_a_backspace(void) {
// 	cgevent_ctrl_key((CGKeyCode)0);  // kVK_ANSI_A
// 	usleep(30000);
//
// 	CGEventRef down = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)51, true);  // kVK_Delete
// 	CGEventRef up   = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)51, false);
//
// 	CGEventPost(kCGSessionEventTap, down);
// 	usleep(20000);
// 	CGEventPost(kCGSessionEventTap, up);
//
// 	CFRelease(down); CFRelease(up);
// }
import "C"

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/c/just-talk-go/internal/clipboard"
)

func pastePlatform(text string, logger *slog.Logger) error {
	cb, err := clipboard.New()
	if err != nil {
		return fmt.Errorf("clipboard: %w", err)
	}
	if err := cb.Set(text); err != nil {
		return fmt.Errorf("set clipboard: %w", err)
	}

	time.Sleep(50 * time.Millisecond)
	if err := simulatePaste(); err != nil {
		return fmt.Errorf("simulate paste: %w", err)
	}
	logger.Debug("autotype done", "text_len", len(text), "method", pasteMethod())
	return nil
}

func simulatePaste() error {
	C.cgevent_cmd_v()
	return nil
}

func sendEnterPlatform(logger *slog.Logger) error {
	C.cgevent_return()
	logger.Debug("send enter done", "method", "darwin/CGEventPost+Return")
	return nil
}

func sendUndoPlatform(logger *slog.Logger) error {
	C.cgevent_ctrl_key((C.CGKeyCode)(6)) // kVK_ANSI_Z
	logger.Debug("undo done", "method", "darwin/CGEventPost+Ctrl+Z")
	return nil
}

func sendClearInputPlatform(logger *slog.Logger) error {
	C.cgevent_ctrl_a_backspace()
	logger.Debug("clear input done", "method", "darwin/CGEventPost+Ctrl+A+Backspace")
	return nil
}

func pasteMethod() string { return "darwin/CGEventPost+Cmd+V" }

func isWaylandSession() bool { return false }
