//go:build linux

package hotkey

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// evdev event types and codes
const (
	evKey      = 0x01
	evSyn      = 0x00
	keyRelease = 0
	keyPress   = 1
	keyRepeat  = 2
)

// input_event struct as defined in <linux/input.h>
type inputEvent struct {
	Time  syscall.Timeval
	Type  uint16
	Code  uint16
	Value int32
}

// Linux input key codes → unified KeyCode.
// These are the standard linux/input-event-codes.h values.
var evdevKeyToUnified = map[uint16]KeyCode{
	// Letters
	16: KeyQ, 17: KeyW, 18: KeyE, 19: KeyR, 20: KeyT,
	21: KeyY, 22: KeyU, 23: KeyI, 24: KeyO, 25: KeyP,
	30: KeyA, 31: KeyS, 32: KeyD, 33: KeyF, 34: KeyG,
	35: KeyH, 36: KeyJ, 37: KeyK, 38: KeyL,
	44: KeyZ, 45: KeyX, 46: KeyC, 47: KeyV, 48: KeyB,
	49: KeyN, 50: KeyM,

	// Digits
	2: Key1, 3: Key2, 4: Key3, 5: Key4, 6: Key5,
	7: Key6, 8: Key7, 9: Key8, 10: Key9, 11: Key0,

	// Numpad
	71: KeyNum7, 72: KeyNum8, 73: KeyNum9,
	74: KeyNumSubtract,
	75: KeyNum4, 76: KeyNum5, 77: KeyNum6,
	78: KeyNumAdd,
	79: KeyNum1, 80: KeyNum2, 81: KeyNum3,
	82: KeyNum0, 83: KeyNumDecimal,

	// Modifiers (left/right merged)
	29: KeyCtrl, 97: KeyCtrl, // LEFTCTRL, RIGHTCTRL
	56: KeyAlt, 100: KeyAlt, // LEFTALT, RIGHTALT
	42: KeyShift, 54: KeyShift, // LEFTSHIFT, RIGHTSHIFT
	125: KeySuper, 126: KeySuper, // LEFTMETA, RIGHTMETA

	// Function keys
	59: KeyF1, 60: KeyF2, 61: KeyF3, 62: KeyF4,
	63: KeyF5, 64: KeyF6, 65: KeyF7, 66: KeyF8,
	67: KeyF9, 68: KeyF10, 87: KeyF11, 88: KeyF12,
	183: KeyF13, 184: KeyF14, 185: KeyF15, 186: KeyF16,
	187: KeyF17, 188: KeyF18, 189: KeyF19, 190: KeyF20,

	// Navigation
	57: KeySpace, 15: KeyTab,
	28: KeyEnter, 1: KeyEscape,
	14: KeyBackspace, 58: KeyCapsLock,
	103: KeyArrowUp, 108: KeyArrowDown,
	105: KeyArrowLeft, 106: KeyArrowRight,
	102: KeyHome, 107: KeyEnd,
	104: KeyPageUp, 109: KeyPageDown,
	110: KeyInsert, 111: KeyDelete,

	// Punctuation
	41: KeyBacktick, 12: KeyMinus, 13: KeyEqual,
	26: KeyLeftBracket, 27: KeyRightBracket,
	43: KeyBackslash, 39: KeySemicolon, 40: KeyQuote,
	51: KeyComma, 52: KeyPeriod, 53: KeySlash,
}

type waylandProvider struct {
	mu       sync.Mutex
	channels map[Combo]chan<- Event
	tracker  *KeyStateTracker
	stopped  bool

	// deviceFds is the list of currently open /dev/input/eventN file
	// descriptors that the readLoop polls. The set is mutated by udev
	// events (add/remove) so it must only be read inside readLoop, but
	// the underlying slice header is read and written under p.mu so
	// readers always see a consistent slice.
	deviceFds []int

	// udevFd is the netlink socket used to listen for input device hotplug
	// events. It is added to the readLoop poll set so that additions and
	// removals are picked up promptly.
	udevFd int

	// wakeFds[0] is read, wakeFds[1] is written. The readLoop blocks in
	// unix.Poll and uses the wake pipe to break out of it when the device
	// set changes underneath.
	wakeFds [2]int

	// stopCh is closed by Stop to ask readLoop to exit. The provider
	// also derives an internal context from the caller's context so
	// Stop can cancel it without depending on the caller cancelling
	// theirs.
	stopCh     chan struct{}
	stopCancel context.CancelFunc
	stopDoneCh chan struct{}

	logger *slog.Logger
}

func newWaylandProvider() (Provider, error) {
	return &waylandProvider{
		channels: make(map[Combo]chan<- Event),
		tracker:  NewKeyStateTracker(),
		stopCh:   make(chan struct{}),
		logger:   slog.Default().With("platform", "wayland"),
	}, nil
}

func (p *waylandProvider) Register(combo Combo) (<-chan Event, error) {
	return p.RegisterWithOptions(combo, RegisterOptions{})
}

func (p *waylandProvider) RegisterWithOptions(combo Combo, opts RegisterOptions) (<-chan Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stopped {
		return nil, fmt.Errorf("provider is stopped")
	}
	if _, exists := p.channels[combo]; exists {
		return nil, fmt.Errorf("hotkey %s already registered", combo)
	}

	ch := make(chan Event, 32)
	p.channels[combo] = ch
	_ = opts
	p.tracker.Watch(combo, ch)
	return ch, nil
}

func (p *waylandProvider) Unregister(combo Combo) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, exists := p.channels[combo]
	if !exists {
		return fmt.Errorf("hotkey %s not registered", combo)
	}

	p.tracker.Unwatch(combo)
	close(ch)
	delete(p.channels, combo)
	return nil
}

func (p *waylandProvider) Start(ctx context.Context) error {
	p.logger.Info("scanning /dev/input for existing keyboard devices")

	// Initial scan picks up devices that were already present at start.
	// This keeps behaviour identical to the previous static implementation
	// for the common case where everything is plugged in before launch.
	if err := p.addAllKeyboardDevicesLocked(); err != nil {
		return fmt.Errorf("scan input devices: %w", err)
	}

	if err := unix.Pipe2(p.wakeFds[:], unix.O_NONBLOCK|unix.O_CLOEXEC); err != nil {
		return fmt.Errorf("create wake pipe: %w", err)
	}

	udevFd, err := openUdevSocket()
	if err != nil {
		_ = unix.Close(p.wakeFds[0])
		_ = unix.Close(p.wakeFds[1])
		// Closing the device fds is acceptable here: Start failed and
		// the caller is expected to abandon the provider.
		p.closeAllDevicesLocked()
		return fmt.Errorf("open udev socket: %w", err)
	}
	p.udevFd = udevFd

	// Derive a context we can cancel from Stop without disturbing the
	// caller's context.
	loopCtx, cancel := context.WithCancel(ctx)
	p.stopCancel = cancel

	// Wake the readLoop when either the caller cancels their context or
	// Stop is invoked locally.
	p.stopDoneCh = make(chan struct{})
	go func() {
		defer close(p.stopDoneCh)
		select {
		case <-loopCtx.Done():
		case <-p.stopCh:
		}
		_, _ = unix.Write(p.wakeFds[1], []byte{1})
	}()

	p.logger.Info("watching for input device hotplug events", "subsystem", "input")
	return p.readLoop(loopCtx)
}

// addAllKeyboardDevicesLocked opens every /dev/input/event* device and
// records the descriptors in p.deviceFds. Devices that already have an
// open fd are skipped, so it is safe to call repeatedly.
func (p *waylandProvider) addAllKeyboardDevicesLocked() error {
	devices, err := findKeyboardDevices()
	if err != nil {
		return fmt.Errorf("find keyboard devices: %w", err)
	}
	if len(devices) == 0 {
		// No devices is not fatal on its own: a user with only a
		// bluetooth keyboard that has not paired yet should still be
		// able to start the daemon and have the device appear later.
		p.logger.Info("no input event devices found at startup; waiting for udev events")
		return nil
	}

	opened := 0
	for _, dev := range devices {
		fd, err := p.openDeviceLocked(dev)
		if err != nil {
			continue
		}
		p.deviceFds = append(p.deviceFds, fd)
		opened++
	}

	p.logger.Info("opened keyboard devices", "count", opened)
	return nil
}

// openDeviceLocked opens a single /dev/input/eventN device, returning
// the fd or an error. The caller is responsible for adding the fd to
// p.deviceFds when appropriate.
func (p *waylandProvider) openDeviceLocked(dev string) (int, error) {
	if p.hasDeviceLocked(dev) {
		return -1, fmt.Errorf("device %s already open", dev)
	}
	fd, err := unix.Open(dev, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		p.logger.Warn("cannot open device", "device", dev, "error", err)
		return -1, err
	}
	p.logger.Info("opened input device", "device", dev)
	return fd, nil
}

func (p *waylandProvider) hasDeviceLocked(dev string) bool {
	for _, fd := range p.deviceFds {
		// Match by underlying device path. Two fds to the same path
		// would be wasteful and can confuse the readLoop dispatch.
		path, _ := readFdPath(fd)
		if path == dev {
			return true
		}
	}
	return false
}

// closeAllDevicesLocked closes every device fd. Used on shutdown.
func (p *waylandProvider) closeAllDevicesLocked() {
	for _, fd := range p.deviceFds {
		_ = unix.Close(fd)
	}
	p.deviceFds = nil
}

func (p *waylandProvider) readLoop(ctx context.Context) error {
	defer func() {
		if p.udevFd >= 0 {
			_ = unix.Close(p.udevFd)
			p.udevFd = -1
		}
		_ = unix.Close(p.wakeFds[0])
		_ = unix.Close(p.wakeFds[1])
	}()

	// Drain the wake pipe so a stale wake from previous reads does not
	// immediately fire on first iteration.
	drainWakeFds(p.wakeFds[0])

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.stopCh:
			return nil
		default:
		}

		pollFds := p.buildPollFdsLocked()

		n, err := unix.Poll(pollFds, -1)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			select {
			case <-p.stopCh:
				return nil
			default:
			}
			return fmt.Errorf("poll keyboard devices: %w", err)
		}
		if n == 0 {
			continue
		}

		// Index 0 is the udev fd, index 1 is the wake pipe read end,
		// indexes 2.. are the input device fds.
		if pollFds[0].Revents != 0 {
			p.handleUdevEvent()
		}
		if pollFds[1].Revents != 0 {
			// Either Stop fired or a wake fired because the device list
			// changed underneath us. Drain and fall through to rebuild
			// the poll set on the next iteration.
			drainWakeFds(p.wakeFds[0])
		}
		for i := 2; i < len(pollFds); i++ {
			if pollFds[i].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) == 0 {
				continue
			}
			p.readAvailableEvents(ctx, int(pollFds[i].Fd))
		}

		// Re-check stop conditions after handling events so a Stop that
		// raced with wake write exits promptly.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.stopCh:
			return nil
		default:
		}
	}
}

// buildPollFdsLocked constructs the PollFd slice for the current set of
// input devices plus the udev fd and wake pipe read end.
func (p *waylandProvider) buildPollFdsLocked() []unix.PollFd {
	out := make([]unix.PollFd, 0, len(p.deviceFds)+2)
	out = append(out, unix.PollFd{Fd: int32(p.udevFd), Events: unix.POLLIN})
	out = append(out, unix.PollFd{Fd: int32(p.wakeFds[0]), Events: unix.POLLIN})
	for _, fd := range p.deviceFds {
		out = append(out, unix.PollFd{Fd: int32(fd), Events: unix.POLLIN})
	}
	return out
}

func (p *waylandProvider) readAvailableEvents(ctx context.Context, fd int) {
	buf := make([]byte, unsafe.Sizeof(inputEvent{}))
	for {
		n, err := unix.Read(fd, buf)
		if err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK || ctx.Err() != nil {
				return
			}
			return
		}
		if n < int(unsafe.Sizeof(inputEvent{})) {
			return
		}
		evt := (*inputEvent)(unsafe.Pointer(&buf[0]))
		p.processEvent(evt)
	}
}

func (p *waylandProvider) processEvent(evt *inputEvent) {
	if evt.Type != evKey {
		return
	}

	key := evdevKeyToUnified[evt.Code]
	if key == KeyNone {
		return
	}

	now := time.Now()

	p.mu.Lock()
	defer p.mu.Unlock()

	var events []Event
	switch evt.Value {
	case keyPress:
		events = p.tracker.KeyDown(key, now)
	case keyRelease:
		events = p.tracker.KeyUp(key, now)
	case keyRepeat:
		// Ignore auto-repeat for now
		return
	}
	for _, e := range events {
		if ch, ok := p.channels[e.Combo]; ok {
			select {
			case ch <- e:
			default:
			}
		}
	}
}

func (p *waylandProvider) Stop() error {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return nil
	}
	p.stopped = true
	close(p.stopCh)
	if p.stopCancel != nil {
		p.stopCancel()
	}
	p.mu.Unlock()

	// Closing device fds while readLoop may still hold them in poll is
	// safe: poll will return POLLHUP/POLLERR and readLoop exits. Do this
	// without holding p.mu to keep lock ordering consistent with the
	// readLoop which acquires p.mu only when reading events.
	p.closeAllDevicesLocked()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Close channels
	for c, ch := range p.channels {
		close(ch)
		delete(p.channels, c)
		p.tracker.Unwatch(c)
	}

	return nil
}

func (p *waylandProvider) Info() ProviderInfo {
	return ProviderInfo{
		Platform: "wayland",
		Backend:  "evdev+udev",
		Features: []string{
			FeatureKeyDown, FeatureKeyUp, FeatureKeyPress,
			FeatureModifierOnly, FeatureFunctionKey, FeatureCombo,
		},
	}
}

// Capture listens for the next key combo the user presses and returns it.
func (p *waylandProvider) Capture(ctx context.Context) (Combo, error) {
	ch := p.tracker.StartCapture()
	if ch == nil {
		return Combo{}, fmt.Errorf("hotkey capture already in progress")
	}
	defer p.tracker.StopCapture()
	select {
	case combo, ok := <-ch:
		if !ok {
			return Combo{}, fmt.Errorf("hotkey capture channel closed")
		}
		return combo, nil
	case <-ctx.Done():
		return Combo{}, ctx.Err()
	}
}

// ---- udev integration ----

// openUdevSocket binds a NETLINK_KOBJECT_UEVENT socket that receives
// kernel uevent broadcasts for all subsystems. We filter for input
// devices in handleUdevEvent.
func openUdevSocket() (int, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return -1, fmt.Errorf("socket: %w", err)
	}
	addr := &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Pid:    0, // kernel-assigned
		Groups: 1, // subscribe to all uevent broadcasts
	}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("bind: %w", err)
	}
	return fd, nil
}

// handleUdevEvent reads all queued netlink messages from the udev socket
// and dispatches add/remove actions for input event devices. Messages are
// never partially parsed: a recv buffer that does not contain a full
// message is left for the next read.
func (p *waylandProvider) handleUdevEvent() {
	buf := make([]byte, 16*1024)
	for {
		n, err := unix.Read(p.udevFd, buf)
		if err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				return
			}
			p.logger.Warn("udev read error", "error", err)
			return
		}
		if n <= 0 {
			return
		}
		msgs := buf[:n]
		for len(msgs) > 0 {
			if len(msgs) < unix.SizeofNlMsghdr {
				return
			}
			hdr := (*unix.NlMsghdr)(unsafe.Pointer(&msgs[0]))
			msgLen := int(hdr.Len)
			if msgLen < unix.SizeofNlMsghdr || msgLen > len(msgs) {
				return
			}
			p.processUdevMessage(msgs[unix.SizeofNlMsghdr:msgLen])
			msgs = msgs[msgLen:]
		}
	}
}

// processUdevMessage inspects a single netlink uevent payload. Payloads
// are NUL-separated KEY=VALUE pairs. We care only about SUBSYSTEM=input
// with a DEVNAME matching /dev/input/eventN.
func (p *waylandProvider) processUdevMessage(payload []byte) {
	action, devname, ok := parseUdevEvent(payload)
	if !ok {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	switch action {
	case "add":
		fd, err := p.openDeviceLocked(devname)
		if err != nil {
			return
		}
		p.deviceFds = append(p.deviceFds, fd)
		p.logger.Info("input device added", "device", devname)
		p.wakeReadLoopLocked()
	case "remove":
		p.removeDeviceLocked(devname)
		p.logger.Info("input device removed", "device", devname)
		p.wakeReadLoopLocked()
	}
}

// parseUdevEvent extracts the action and DEVNAME from a netlink uevent
// payload, returning ok=false when the payload is for a different
// subsystem or is otherwise uninteresting.
func parseUdevEvent(payload []byte) (action, devname string, ok bool) {
	fields := splitNulFields(payload)
	if len(fields) == 0 {
		return "", "", false
	}
	// First field is "action@devpath", e.g. "add@/devices/.../input/input23".
	head := fields[0]
	at := strings.IndexByte(head, '@')
	if at < 0 {
		return "", "", false
	}
	action = head[:at]
	if action != "add" && action != "remove" {
		return "", "", false
	}

	var subsystem string
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "SUBSYSTEM=") {
			subsystem = f[len("SUBSYSTEM="):]
		} else if strings.HasPrefix(f, "DEVNAME=") {
			devname = f[len("DEVNAME="):]
		}
	}
	if subsystem != "input" {
		return "", "", false
	}
	if devname == "" {
		return "", "", false
	}
	if !strings.HasPrefix(devname, "/dev/input/event") {
		return "", "", false
	}
	return action, devname, true
}

// removeDeviceLocked closes the fd pointing at dev (if any) and removes
// it from p.deviceFds. Safe to call when the device is not open.
func (p *waylandProvider) removeDeviceLocked(dev string) {
	for i, fd := range p.deviceFds {
		path, err := readFdPath(fd)
		if err == nil && path == dev {
			_ = unix.Close(fd)
			p.deviceFds = append(p.deviceFds[:i], p.deviceFds[i+1:]...)
			return
		}
	}
}

// readFdPath returns the path the given fd points at, by reading the
// /proc/self/fd/N symlink. Returns an empty string and an error if the
// fd is invalid or the path cannot be read.
func readFdPath(fd int) (string, error) {
	target, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
	if err != nil {
		return "", err
	}
	return target, nil
}

// wakeReadLoopLocked nudges the readLoop out of poll so it can rebuild
// its fd set against the updated p.deviceFds slice. Caller must hold p.mu.
func (p *waylandProvider) wakeReadLoopLocked() {
	if p.wakeFds[1] < 0 {
		return
	}
	_, _ = unix.Write(p.wakeFds[1], []byte{1})
}

// splitNulFields splits a NUL-separated byte slice into a slice of
// strings. Empty trailing fields (which the kernel always appends) are
// discarded.
func splitNulFields(b []byte) []string {
	var out []string
	for {
		i := bytes.IndexByte(b, 0)
		if i < 0 {
			if len(b) > 0 {
				out = append(out, string(b))
			}
			return out
		}
		if i > 0 {
			out = append(out, string(b[:i]))
		}
		b = b[i+1:]
	}
}

func drainWakeFds(fd int) {
	const max = 64
	var drained [max]byte
	for {
		_, err := unix.Read(fd, drained[:])
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			return
		}
		if err != nil {
			return
		}
	}
}

// ---- Device discovery (initial scan) ----

// findKeyboardDevices scans /dev/input/event* for keyboard devices.
// It is used for the initial scan at Start() and is not the source of
// truth once the udev listener is attached.
func findKeyboardDevices() ([]string, error) {
	entries, err := os.ReadDir("/dev/input/")
	if err != nil {
		return nil, fmt.Errorf("read /dev/input/: %w", err)
	}

	var devices []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "event") {
			continue
		}
		devPath := filepath.Join("/dev/input", entry.Name())

		devices = append(devices, devPath)
	}

	return devices, nil
}
