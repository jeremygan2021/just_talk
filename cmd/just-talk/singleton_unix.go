package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

const singletonLockName = "just-talk.lock"

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
// On success it returns a non-nil lock that the caller must hold for the
// lifetime of the daemon. On failure it returns an error describing why
// another instance is already running.
//
// When a previous instance crashed without releasing the lock, the new
// process steals the stale lock instead of refusing to start, so that a
// crash never permanently disables Just Talk.
func acquireSingleton() (*singletonLock, error) {
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

	pid := os.Getpid()
	if _, err := fmt.Fprintf(f, "%d\n", pid); err != nil {
		f.Close()
		return nil, fmt.Errorf("write pid to lock: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, fmt.Errorf("sync lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		stale, _ := isStaleLock(f)
		f.Close()
		if stale {
			if err2 := os.Remove(path); err2 != nil {
				return nil, fmt.Errorf("remove stale lock: %w", err2)
			}
			return acquireSingleton()
		}
		return nil, fmt.Errorf("another just-talk instance is already running (lock held by %s)", path)
	}

	return &singletonLock{path: path, file: f}, nil
}

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
