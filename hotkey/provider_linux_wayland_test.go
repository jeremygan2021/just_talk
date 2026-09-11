package hotkey

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSplitNulFields(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want []string
	}{
		{"empty", []byte{}, nil},
		{"single no nul", []byte("hello"), []string{"hello"}},
		{"two fields", []byte("a\x00b\x00"), []string{"a", "b"}},
		{"trailing empty", []byte("a\x00b\x00\x00"), []string{"a", "b"}},
		{"leading empty", []byte("\x00a\x00b\x00"), []string{"a", "b"}},
		{"uevent shape", []byte("add@/devices/pci/.../input/input23\x00SUBSYSTEM=input\x00DEVNAME=/dev/input/event23\x00\x00"), []string{
			"add@/devices/pci/.../input/input23",
			"SUBSYSTEM=input",
			"DEVNAME=/dev/input/event23",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitNulFields(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("splitNulFields(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitNulFields(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestProcessUdevMessageFiltersNonInput(t *testing.T) {
	p := newTestWaylandProvider(t)
	// add action but SUBSYSTEM != input should be ignored
	p.processUdevMessage([]byte("add@/devices/foo\x00SUBSYSTEM=usb\x00DEVNAME=/dev/bus/usb/001/002\x00\x00"))
	if len(p.deviceFds) != 0 {
		t.Fatalf("non-input event was processed; deviceFds=%v", p.deviceFds)
	}
}

func TestProcessUdevMessageIgnoresNonAddRemoveActions(t *testing.T) {
	p := newTestWaylandProvider(t)
	p.processUdevMessage([]byte("change@/devices/pci/.../input/input23\x00SUBSYSTEM=input\x00DEVNAME=/dev/input/event23\x00\x00"))
	if len(p.deviceFds) != 0 {
		t.Fatalf("change action was processed; deviceFds=%v", p.deviceFds)
	}
}

func TestProcessUdevMessageIgnoresMalformedPayload(t *testing.T) {
	p := newTestWaylandProvider(t)
	// Missing '@' separator in head field
	p.processUdevMessage([]byte("add_no_at_sign\x00SUBSYSTEM=input\x00DEVNAME=/dev/input/event23\x00\x00"))
	if len(p.deviceFds) != 0 {
		t.Fatalf("malformed payload was processed; deviceFds=%v", p.deviceFds)
	}
}

func TestProcessUdevMessageFiltersByDevname(t *testing.T) {
	p := newTestWaylandProvider(t)
	// SUBSYSTEM=input but DEVNAME outside /dev/input/event* — not a
	// device we care about (e.g. /dev/input/mouseN).
	p.processUdevMessage([]byte("add@/devices/.../input/input23\x00SUBSYSTEM=input\x00DEVNAME=/dev/input/mouse0\x00\x00"))
	if len(p.deviceFds) != 0 {
		t.Fatalf("non-event DEVNAME was processed; deviceFds=%v", p.deviceFds)
	}
}

func TestProcessUdevMessageRemovesKnownDevice(t *testing.T) {
	p := newTestWaylandProvider(t)
	// Bypass the SUBSYSTEM/DEVNAME filter in processUdevMessage by going
	// through the lower-level helpers directly. We use a regular file as
	// a stand-in for an evdev device: unix.Open is happy with any path,
	// and removeDeviceLocked matches by /proc/self/fd/N.
	fake := "/tmp/just-talk-test-evdev-fake"
	if err := os.WriteFile(fake, []byte("x"), 0o600); err != nil {
		t.Fatalf("create fake device: %v", err)
	}
	defer os.Remove(fake)

	p.mu.Lock()
	fd, err := p.openDeviceLocked(fake)
	if err != nil {
		p.mu.Unlock()
		t.Fatalf("open fake device: %v", err)
	}
	p.deviceFds = append(p.deviceFds, fd)
	p.mu.Unlock()

	if got := len(p.deviceFds); got != 1 {
		t.Fatalf("after add: deviceFds len=%d, want 1", got)
	}

	p.mu.Lock()
	p.removeDeviceLocked(fake)
	p.mu.Unlock()

	if got := len(p.deviceFds); got != 0 {
		t.Fatalf("after remove: deviceFds len=%d, want 0", got)
	}
}

func TestRemoveDeviceLockedNoOpWhenUnknown(t *testing.T) {
	p := newTestWaylandProvider(t)
	// Should not panic or error when called with a device that was never
	// opened.
	p.removeDeviceLocked("/dev/input/event99999")
	if len(p.deviceFds) != 0 {
		t.Fatalf("deviceFds unexpectedly non-empty: %v", p.deviceFds)
	}
}

func TestParseUdevEvent(t *testing.T) {
	cases := []struct {
		name       string
		payload    []byte
		wantAction string
		wantDev    string
		wantOK     bool
	}{
		{
			name:    "empty",
			payload: nil,
			wantOK:  false,
		},
		{
			name:    "missing at sign",
			payload: []byte("add_no_at\x00SUBSYSTEM=input\x00DEVNAME=/dev/input/event23\x00"),
			wantOK:  false,
		},
		{
			name:       "add input event",
			payload:    []byte("add@/devices/pci/.../input/input23\x00SUBSYSTEM=input\x00DEVNAME=/dev/input/event23\x00\x00"),
			wantAction: "add",
			wantDev:    "/dev/input/event23",
			wantOK:     true,
		},
		{
			name:       "remove input event",
			payload:    []byte("remove@/devices/pci/.../input/input23\x00SUBSYSTEM=input\x00DEVNAME=/dev/input/event23\x00\x00"),
			wantAction: "remove",
			wantDev:    "/dev/input/event23",
			wantOK:     true,
		},
		{
			name:    "non-input subsystem",
			payload: []byte("add@/x\x00SUBSYSTEM=usb\x00DEVNAME=/dev/input/event23\x00\x00"),
			wantOK:  false,
		},
		{
			name:    "non-event devname",
			payload: []byte("add@/x\x00SUBSYSTEM=input\x00DEVNAME=/dev/input/mouse0\x00\x00"),
			wantOK:  false,
		},
		{
			name:    "change action",
			payload: []byte("change@/x\x00SUBSYSTEM=input\x00DEVNAME=/dev/input/event23\x00\x00"),
			wantOK:  false,
		},
		{
			name:    "missing devname",
			payload: []byte("add@/x\x00SUBSYSTEM=input\x00\x00"),
			wantOK:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotAction, gotDev, gotOK := parseUdevEvent(tc.payload)
			if gotOK != tc.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if gotAction != tc.wantAction {
				t.Fatalf("action = %q, want %q", gotAction, tc.wantAction)
			}
			if gotDev != tc.wantDev {
				t.Fatalf("devname = %q, want %q", gotDev, tc.wantDev)
			}
		})
	}
}

// newTestWaylandProvider builds a provider that is safe to call the
// udev-message helpers on without going through Start(). It does not
// create a real udev socket and never enters readLoop.
func newTestWaylandProvider(t *testing.T) *waylandProvider {
	t.Helper()
	p, err := newWaylandProvider()
	if err != nil {
		t.Fatalf("newWaylandProvider: %v", err)
	}
	wp := p.(*waylandProvider)
	wp.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	return wp
}

// TestOpenUdevSocket verifies that the NETLINK_KOBJECT_UEVENT socket
// used by readLoop can be created and bound in this environment. Some
// sandboxes (and CI containers without CAP_NET_ADMIN) refuse the bind;
// we skip rather than fail in that case.
func TestOpenUdevSocket(t *testing.T) {
	fd, err := openUdevSocket()
	if err != nil {
		t.Skipf("cannot bind NETLINK_KOBJECT_UEVENT in this environment: %v", err)
	}
	defer unix.Close(fd)
	if fd < 0 {
		t.Fatalf("openUdevSocket returned fd=%d, want >=0", fd)
	}
}

// TestStartPicksUpInitialDevices is a coarse smoke test: Start must not
// return an error on a system where /dev/input/event* is reachable, and
// Stop must unblock Start. Start blocks until ctx cancel or Stop, so the
// test runs Start in a goroutine.
func TestStartPicksUpInitialDevices(t *testing.T) {
	if _, err := os.Stat("/dev/input/event0"); err != nil {
		t.Skipf("/dev/input not available: %v", err)
	}

	p := newTestWaylandProvider(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- p.Start(ctx)
	}()

	// Give readLoop a moment to enter poll.
	time.Sleep(50 * time.Millisecond)

	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	select {
	case err := <-done:
		// Start is documented to return ctx.Err() when the context is
		// cancelled. Stop cancels the derived loop context, so
		// context.Canceled is the expected outcome here.
		if err != nil && err != context.Canceled {
			t.Fatalf("Start returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
}
