package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const (
	singletonLockName = "just-talk.lock"
	// killGraceWindow is how long acquireSingleton waits for a
	// previous daemon to exit on its own (SIGTERM) before resorting to
	// SIGKILL. Kept short so a misbehaving previous daemon never
	// blocks a new launch for more than this many seconds.
	killGraceWindow = 2 * time.Second
	// killPollInterval is how often we poll the previous PID while
	// waiting for it to exit. Cheap; signals kill the process
	// asynchronously.
	killPollInterval = 50 * time.Millisecond
)

// instanceMode records how the lock holder is running. It decides
// whether a new launch may take the lock over: background daemons are
// silent and safe to replace, an interactive TUI is not.
type instanceMode string

const (
	modeDaemon instanceMode = "daemon"
	modeTUI    instanceMode = "tui"
)

// singletonLock represents an acquired single-instance lock.
//
// Lock is a held flock(2) on a file inside the user's runtime directory.
// The lock is released by the OS when the process exits, but callers
// should still call Release explicitly so that the file is unlinked.
type singletonLock struct {
	path   string
	file   *os.File
	stolen bool
}

// acquireSingleton attempts to lock the just-talk runtime file.
//
// `mode` describes the caller (daemon or TUI) and is recorded in the lock
// file so a later launch knows what it is replacing.
//
// On success it returns a non-nil lock that the caller must hold for its
// whole lifetime.
//
// On failure three outcomes are possible:
//   - The lock is stale (the previous PID is dead, e.g. the previous
//     instance crashed). The lock file is removed and acquisition is
//     retried.
//   - The lock is held by a running *daemon*. It is asked to exit
//     (SIGTERM, then SIGKILL after killGraceWindow), the lock file is
//     removed, and acquisition is retried so the new launch takes over the
//     global hotkeys and clipboard dispatch path.
//   - The lock is held by a running *TUI*. Interactive instances are never
//     killed (that would garble the user's terminal), so an error is
//     returned instead.
//   - The lock cannot be opened (permission denied, etc.). An error is
//     returned and the caller must decide what to do.
func acquireSingleton(mode instanceMode) (*singletonLock, error) {
	dir, err := runtimeDir()
	if err != nil {
		return nil, fmt.Errorf("locate runtime dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create runtime dir: %w", err)
	}
	path := filepath.Join(dir, singletonLockName)

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	// IMPORTANT: do NOT write our PID yet. If we wrote first, we'd
	// overwrite the previous holder's PID before we read it back to
	// decide whether to kill the previous daemon. The lock file is
	// a record of who is *currently* holding the lock; we become
	// that only after flock(2) returns success.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		if err := writeLockPID(f, os.Getpid(), mode); err != nil {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
			return nil, fmt.Errorf("write pid to lock: %w", err)
		}
		return &singletonLock{path: path, file: f}, nil
	} else {
		// Could not acquire the lock. Decide whether the holder is
		// alive, and if so whether we may replace it.
		prevPID, prevMode, stale := lockFileState(f)
		f.Close()
		if stale {
			if err2 := os.Remove(path); err2 != nil {
				return nil, fmt.Errorf("remove stale lock: %w", err2)
			}
			return acquireSingleton(mode)
		}
		if prevMode == modeTUI {
			return nil, fmt.Errorf("another just-talk is already running in TUI mode (pid %d); close it first", prevPID)
		}
		if err := stopPreviousInstance(prevPID, path); err != nil {
			if err == errSelfLocked {
				return nil, fmt.Errorf("another just-talk instance is already running in this process (lock %s)", path)
			}
			return nil, fmt.Errorf("previous instance (pid %d) refused to exit: %w", prevPID, err)
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove lock after kill: %w", err)
		}
		return acquireSingleton(mode)
	}
}

// writeLockPID truncates the lock file and writes "<pid> <mode>". It is
// called only after flock(2) has succeeded, so the file contents reliably
// identify the current holder.
func writeLockPID(f *os.File, pid int, mode instanceMode) error {
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "%d %s\n", pid, mode); err != nil {
		return err
	}
	return f.Sync()
}

// stopPreviousInstance asks the previous just-talk daemon to exit and
// waits up to killGraceWindow for it to release its flock. If it does
// not exit on its own, SIGKILL is sent as a last resort.
//
// The caller is expected to remove the lock file after this returns.
//
// If the recorded PID belongs to the current process (a reentrant
// acquire from inside the same Go runtime) the function returns
// errSelfLocked; acquireSingleton surfaces this as a clear
// "already running" error rather than recursively trying to kill the
// caller.
func stopPreviousInstance(pid int, lockPath string) error {
	if pid == os.Getpid() {
		return errSelfLocked
	}
	if pid <= 0 {
		return nil
	}
	fmt.Fprintf(os.Stderr, "just-talk: replacing previous instance (pid %d, lock %s)\n", pid, lockPath)

	// phaseIsGone encapsulates "is this PID effectively dead yet".
	// /proc/<pid>/status is more reliable than kill(pid, 0) here:
	// after SIGKILL the process becomes a zombie whose PID is kept
	// around until the original parent reaps it. From our point of
	// view the daemon is gone the moment it stops running, so we
	// accept either ESRCH or State=="Z" as success.
	phaseIsGone := func() bool {
		if err := syscall.Kill(pid, 0); err != nil {
			return err == syscall.ESRCH
		}
		if state, err := readProcState(pid); err == nil && state == "Z" {
			return true
		}
		return false
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return nil
		}
		if err == syscall.EPERM {
			return fmt.Errorf("no permission to signal (different uid?): %w", err)
		}
		return fmt.Errorf("SIGTERM: %w", err)
	}

	deadline := time.Now().Add(killGraceWindow)
	for time.Now().Before(deadline) {
		if phaseIsGone() {
			return nil
		}
		time.Sleep(killPollInterval)
	}

	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		if err == syscall.ESRCH {
			return nil
		}
		return fmt.Errorf("SIGKILL: %w", err)
	}
	// Give the kernel a moment to deliver SIGKILL and (ideally)
	// reap. phaseIsGone still accepts zombie as success.
	for i := 0; i < 20; i++ {
		if phaseIsGone() {
			return nil
		}
		time.Sleep(killPollInterval)
	}
	return fmt.Errorf("pid %d still alive after SIGKILL", pid)
}

// readProcState reads the single-letter State field from
// /proc/<pid>/status. Returns an error if the file is missing or
// cannot be parsed (which usually means the process has been reaped).
func readProcState(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return "", err
	}
	for _, line := range bytesSplitLines(data) {
		if len(line) >= 6 && bytes.HasPrefix(line, []byte("State:")) {
			rest := line[6:]
			// trim leading whitespace
			i := 0
			for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
				i++
			}
			if i < len(rest) {
				return string(rest[i : i+1]), nil
			}
		}
	}
	return "", fmt.Errorf("no State field for pid %d", pid)
}

// bytesSplitLines splits on \n without importing bytes for one call.
func bytesSplitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// errSelfLocked is returned by stopPreviousInstance when the lock
// file's recorded PID is the current process. acquireSingleton turns
// this into a user-visible error instead of recursing.
var errSelfLocked = fmt.Errorf("lock already held by this process")

// Release drops the flock and unlinks the lock file. Safe to call on a
// nil receiver; subsequent calls are no-ops.
func (l *singletonLock) Release() {
	if l == nil {
		return
	}
	if l.file != nil {
		_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		_ = l.file.Close()
		l.file = nil
	}
	if l.path != "" {
		_ = os.Remove(l.path)
	}
}

// lockFileState reads "<pid> <mode>" from the start of f and reports the
// holder and whether the lock is stale (PID missing / not a positive
// integer / no longer alive). On stale locks the returned pid is
// meaningless; callers should not act on it.
//
// Lock files written by older builds only contain the PID; those are
// reported as modeDaemon so they keep the historical replace behaviour.
func lockFileState(f *os.File) (pid int, mode instanceMode, stale bool) {
	if _, err := f.Seek(0, 0); err != nil {
		return 0, modeDaemon, true
	}
	var read int
	var modeText string
	if _, err := fmt.Fscanln(f, &read, &modeText); err != nil && modeText == "" {
		// Fscanln reports an error when the trailing newline is missing
		// but still fills in what it read; only fail when nothing was
		// parsed at all.
		if read <= 0 {
			return 0, modeDaemon, true
		}
	}
	mode = modeDaemon
	if modeText == string(modeTUI) {
		mode = modeTUI
	}
	if read <= 0 {
		return 0, mode, true
	}
	if err := syscall.Kill(read, 0); err == nil {
		return read, mode, false
	} else if err == syscall.ESRCH {
		return read, mode, true
	}
	// EPERM or other unexpected error: treat as alive so the caller
	// can decide whether to escalate.
	return read, mode, false
}

// isStaleLock keeps the old single-return-value contract used by tests
// and external callers. It returns true when the lock file's recorded
// PID is missing or no longer alive.
func isStaleLock(f *os.File) (bool, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return false, err
	}
	var pid int
	_, err := fmt.Fscanln(f, &pid)
	if err != nil {
		return true, nil
	}
	if pid <= 0 {
		return true, nil
	}
	if err := syscall.Kill(pid, 0); err == nil {
		return false, nil
	} else if err == syscall.ESRCH {
		return true, nil
	}
	return false, err
}

func runtimeDir() (string, error) {
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "just-talk"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "just-talk"), nil
}

// mustParsePID is a small helper used by tests; production code reads
// the pid via Fscanln directly.
func mustParsePID(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
