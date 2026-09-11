package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSingletonLockAcquiredAndReentrant(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	lock1, err := acquireSingleton(modeDaemon)
	if err != nil {
		t.Fatalf("first acquireSingleton failed: %v", err)
	}
	defer lock1.Release()

	// Second call inside the same process must refuse (the lock
	// file's PID is us). It must NOT silently kill the caller.
	_, err = acquireSingleton(modeDaemon)
	if err == nil {
		t.Fatalf("second acquireSingleton should fail while first is held")
	}
	if !strings.Contains(err.Error(), "this process") && !strings.Contains(err.Error(), "already running") {
		t.Fatalf("unexpected reentrant error: %v", err)
	}

	lock1.Release()
	lock2, err := acquireSingleton(modeDaemon)
	if err != nil {
		t.Fatalf("acquireSingleton after release failed: %v", err)
	}
	defer lock2.Release()
}

func TestStaleLockReplacement(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	lockPath := filepath.Join(dir, "just-talk", singletonLockName)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte("999999\n"), 0644); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}

	// A second TUI may also replace a stale lock: liveness, not mode,
	// decides whether a lock is stealable.
	lock, err := acquireSingleton(modeTUI)
	if err != nil {
		t.Fatalf("acquireSingleton should steal stale lock, got: %v", err)
	}
	defer lock.Release()
}

func TestAcquireSingletonNilReceiverRelease(t *testing.T) {
	var l *singletonLock
	l.Release() // must not panic
}

// TestStopPreviousInstanceKillsTarget verifies the kill path
// directly: spawn a long-running child, point the lock file at it,
// call stopPreviousInstance, then reap the zombie (as the original
// launcher would in a real second-launch scenario) and confirm the
// PID is freed.
func TestStopPreviousInstanceKillsTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a real subprocess; skipped in -short mode")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	pid := cmd.Process.Pid
	// Reap the child ourselves so the test cleanup does not race
	// with stopPreviousInstance's SIGKILL on the same PID. We do
	// the Wait after stopPreviousInstance returns so the test
	// exercises the realistic flow: external launcher reaps after
	// the daemon exits.
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})

	if err := stopPreviousInstance(pid, "test"); err != nil {
		t.Fatalf("stopPreviousInstance: %v", err)
	}

	// Simulate the original parent (the launcher that started the
	// previous daemon) reaping the zombie. Until it does, kill(pid, 0)
	// keeps returning success.
	if _, err := cmd.Process.Wait(); err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		t.Logf("reap after stopPreviousInstance: %v", err)
	}

	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("child still alive after stopPreviousInstance + reap")
	} else if !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("pid probe returned %v, want ESRCH", err)
	}
}

// TestStopPreviousInstanceIgnoresStalePID ensures we do not try to
// kill a process whose PID has already been reaped.
func TestStopPreviousInstanceIgnoresStalePID(t *testing.T) {
	// 999999 is virtually never a live PID on a healthy system.
	if err := stopPreviousInstance(999999, "test"); err != nil {
		t.Fatalf("stopPreviousInstance(stale pid): %v", err)
	}
}

// TestStopPreviousInstanceRefusesSelf ensures the function refuses to
// signal its own PID instead of recursing into a self-kill loop.
func TestStopPreviousInstanceRefusesSelf(t *testing.T) {
	err := stopPreviousInstance(os.Getpid(), "test")
	if !errors.Is(err, errSelfLocked) {
		t.Fatalf("stopPreviousInstance(self): err=%v, want errSelfLocked", err)
	}
}

// TestAcquireSingletonReplacesUnresponsiveDaemon verifies the SIGKILL
// fallback: a previous daemon that ignores SIGTERM (we use SIG_IGN
// via a small bash wrapper) is still taken down within
// killGraceWindow + a small grace for the kernel to reap it.
func TestAcquireSingletonReplacesUnresponsiveDaemon(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a real subprocess; skipped in -short mode")
	}
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	lockPath := filepath.Join(dir, "just-talk", singletonLockName)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// A bash subshell that ignores SIGTERM (trap '' TERM) and waits.
	cmd := exec.Command("bash", "-c", "trap '' TERM; sleep 30 & wait $!")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn unresponsive daemon: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})
	if err := os.WriteFile(lockPath, []byte(strconvItoa(pid)+" daemon\n"), 0644); err != nil {
		t.Fatalf("seed lock file: %v", err)
	}

	start := time.Now()
	lock, err := acquireSingleton(modeDaemon)
	if err != nil {
		t.Fatalf("acquireSingleton should replace unresponsive daemon via SIGKILL, got: %v", err)
	}
	defer lock.Release()
	if elapsed := time.Since(start); elapsed > killGraceWindow+3*time.Second {
		t.Fatalf("replace took %v, expected < %v + slack", elapsed, killGraceWindow+3*time.Second)
	}
}

// strconvItoa is a tiny shim so the test file doesn't need to import
// strconv just for one Itoa call.
// TestAcquireSingletonReplacesDaemonForTUI makes sure opening the TUI
// takes over a background daemon: the daemon is silent, so replacing it
// is safe and keeps the user from having to hunt down a stale process.
func TestAcquireSingletonReplacesDaemonForTUI(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a real subprocess; skipped in -short mode")
	}
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	lockPath := filepath.Join(dir, "just-talk", singletonLockName)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})
	if err := os.WriteFile(lockPath, []byte(strconvItoa(pid)+" daemon\n"), 0644); err != nil {
		t.Fatalf("seed lock file: %v", err)
	}

	lock, err := acquireSingleton(modeTUI)
	if err != nil {
		t.Fatalf("TUI should take over a background daemon, got: %v", err)
	}
	defer lock.Release()
}

// TestAcquireSingletonRefusesLiveTUI is the other half of the rule: an
// interactive TUI is never force-killed, because that would leave its
// terminal in raw/alt-screen mode. A new launch must report the conflict
// instead - and refusing is what prevents the duplicate-paste bug, since
// two live instances both record and both paste.
func TestAcquireSingletonRefusesLiveTUI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	lockPath := filepath.Join(dir, "just-talk", singletonLockName)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Impersonate a live TUI: hold the flock and record our own (alive)
	// PID together with the tui mode.
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("flock: %v", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if _, err := fmt.Fprintf(f, "%d %s\n", os.Getpid(), modeTUI); err != nil {
		t.Fatalf("write lock file: %v", err)
	}

	for _, mode := range []instanceMode{modeDaemon, modeTUI} {
		if _, err := acquireSingleton(mode); err == nil {
			t.Fatalf("acquireSingleton(%s) must refuse a live TUI", mode)
		} else if !strings.Contains(err.Error(), "TUI") {
			t.Fatalf("acquireSingleton(%s) error = %v, want a TUI conflict message", mode, err)
		}
	}
}

// TestLockFileStateTreatsLegacyPIDAsDaemon keeps backwards compatibility
// with lock files written before the mode field existed.
func TestLockFileStateTreatsLegacyPIDAsDaemon(t *testing.T) {
	cases := []struct {
		content string
		want    instanceMode
	}{
		{fmt.Sprintf("%d\n", os.Getpid()), modeDaemon},
		{fmt.Sprintf("%d daemon\n", os.Getpid()), modeDaemon},
		{fmt.Sprintf("%d tui\n", os.Getpid()), modeTUI},
	}
	for _, tc := range cases {
		path := filepath.Join(t.TempDir(), "lock")
		if err := os.WriteFile(path, []byte(tc.content), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		pid, mode, stale := lockFileState(f)
		f.Close()
		if stale {
			t.Fatalf("lockFileState(%q) reported stale", tc.content)
		}
		if pid != os.Getpid() {
			t.Fatalf("lockFileState(%q) pid = %d, want %d", tc.content, pid, os.Getpid())
		}
		if mode != tc.want {
			t.Fatalf("lockFileState(%q) mode = %q, want %q", tc.content, mode, tc.want)
		}
	}
}

// strconvItoa is a tiny shim so the test file doesn't need to import
// strconv just for one Itoa call.
func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
