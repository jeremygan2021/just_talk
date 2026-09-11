package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSingletonLockAcquiredAndReentrant(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	lock1, err := acquireSingleton()
	if err != nil {
		t.Fatalf("first acquireSingleton failed: %v", err)
	}
	defer lock1.Release()

	if _, err := acquireSingleton(); err == nil {
		t.Fatalf("second acquireSingleton should fail while first is held")
	}

	lock1.Release()
	lock2, err := acquireSingleton()
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

	lock, err := acquireSingleton()
	if err != nil {
		t.Fatalf("acquireSingleton should steal stale lock, got: %v", err)
	}
	defer lock.Release()
}

func TestAcquireSingletonNilReceiverRelease(t *testing.T) {
	var l *singletonLock
	l.Release() // must not panic
}
