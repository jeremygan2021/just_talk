package hotkey

import "context"

// Features that a provider may support.
const (
	FeatureKeyDown       = "keydown"       // Supports KeyDown events
	FeatureKeyUp         = "keyup"         // Supports KeyUp events
	FeatureKeyPress      = "keypress"      // Supports KeyPress events
	FeatureModifierOnly  = "modifier-only" // Supports modifier-only combos (e.g., just Ctrl)
	FeatureFunctionKey   = "function-key"  // Supports function-key-only combos (e.g., just F1)
	FeatureCombo         = "combo"         // Supports regular modifier+key combos
	FeatureSuppressEvent = "suppress"      // Can suppress/consume events (block from reaching other apps)
)

// Provider is the platform-specific global hotkey backend.
//
// Each platform has its own implementation:
//   - Windows:  RegisterHotKey + SetWindowsHookEx
//   - macOS:    CGEventTap
//   - Linux X11: XGrabKey + X event loop
//   - Linux Wayland: evdev (/dev/input/event*) or XDG Desktop Portal
//
// A Provider implementation is not safe for concurrent use; the Registry
// handles synchronization.
type Provider interface {
	// Register registers a hotkey combo and returns a channel that receives
	// events when the combo is triggered. The channel is unbuffered — the
	// consumer must read promptly to avoid blocking the provider's event loop.
	//
	// Registering the same combo twice returns an error.
	Register(combo Combo) (<-chan Event, error)

	// RegisterWithOptions registers a hotkey combo with provider-specific
	// behavior such as suppressing the original key events when supported.
	RegisterWithOptions(combo Combo, opts RegisterOptions) (<-chan Event, error)

	// Unregister removes a previously registered hotkey combo.
	// The event channel is closed.
	Unregister(combo Combo) error

	// Start begins listening for hotkey events. This call blocks until the
	// context is cancelled or Stop() is called. The provider should spawn
	// its event loop in this call.
	//
	// Start must be called after all Register calls.
	Start(ctx context.Context) error

	// Stop signals the provider to stop listening. It should cause Start()
	// to return. Idempotent — calling Stop multiple times is safe.
	Stop() error

	// Info returns metadata about the provider.
	Info() ProviderInfo

	// Capture blocks until the user presses a key combo or ctx is cancelled.
	// It returns the captured Combo. Implementations must surface the full
	// modifier mask of any keys currently held when the trigger key goes
	// down. A bare modifier release (e.g. tapping Ctrl by itself) yields a
	// Combo with Key == KeyNone and Mods reflecting the released modifier.
	// Capture is single-shot; each call returns exactly one Combo.
	Capture(ctx context.Context) (Combo, error)
}

type RegisterOptions struct {
	Suppress bool
}

// ProviderInfo describes a provider implementation.
type ProviderInfo struct {
	Platform string   // "x11", "wayland", "darwin", "windows", "mock"
	Backend  string   // "CGEventTap", "XGrabKey", "evdev", "RegisterHotKey", "mock"
	Features []string // List of supported Feature constants
}

// HasFeature checks if the provider supports a given feature.
func (pi ProviderInfo) HasFeature(feature string) bool {
	for _, f := range pi.Features {
		if f == feature {
			return true
		}
	}
	return false
}
