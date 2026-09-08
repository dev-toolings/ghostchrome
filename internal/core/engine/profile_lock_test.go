package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func writeSingletonLock(t *testing.T, dir, target string) string {
	t.Helper()
	lock := filepath.Join(dir, "SingletonLock")
	if err := os.Symlink(target, lock); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	return lock
}

func TestClearStaleProfileLockRemovesDeadOwner(t *testing.T) {
	dir := t.TempDir()
	// A reaped child is a PID that is guaranteed gone, unlike a guessed number.
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run probe process: %v", err)
	}
	deadPID := cmd.Process.Pid

	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	lock := writeSingletonLock(t, dir, fmt.Sprintf("%s-%d", host, deadPID))

	clearStaleProfileLock(dir)

	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("stale lock still present (lstat err = %v)", err)
	}
}

func TestClearStaleProfileLockKeepsLiveOwner(t *testing.T) {
	dir := t.TempDir()
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	lock := writeSingletonLock(t, dir, fmt.Sprintf("%s-%d", host, os.Getpid()))

	clearProfileLockWithin(dir, 0)

	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("live owner's lock was removed: %v", err)
	}
}

// A session that was just stopped still has a Chrome winding down, so the
// cleanup must wait for the owner instead of giving up on the first check.
func TestClearProfileLockWaitsForOwnerToExit(t *testing.T) {
	dir := t.TempDir()
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	cmd := exec.Command("sh", "-c", "sleep 0.3")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start probe process: %v", err)
	}
	// Reap the child so its PID really disappears from the process table.
	go func() { _ = cmd.Wait() }()
	lock := writeSingletonLock(t, dir, fmt.Sprintf("%s-%d", host, cmd.Process.Pid))

	clearProfileLockWithin(dir, 0)
	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("lock of a still-running owner was removed: %v", err)
	}

	clearProfileLockWithin(dir, 3*time.Second)
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("lock still present after the owner exited (lstat err = %v)", err)
	}
}

func TestClearStaleProfileLockKeepsForeignHostAndGarbage(t *testing.T) {
	for _, target := range []string{"some-other-machine-1", "not-a-lock", "host-"} {
		dir := t.TempDir()
		lock := writeSingletonLock(t, dir, target)

		clearProfileLockWithin(dir, 0)

		if _, err := os.Lstat(lock); err != nil {
			t.Fatalf("lock %q was removed: %v", target, err)
		}
	}
}

func TestClearStaleProfileLockIgnoresMissingProfile(t *testing.T) {
	clearStaleProfileLock("")
	clearStaleProfileLock(filepath.Join(t.TempDir(), "does-not-exist"))
}

func TestParseSingletonLock(t *testing.T) {
	tests := []struct {
		target   string
		wantHost string
		wantPID  int
		wantOK   bool
	}{
		{"kev-MS-7B85-2133384", "kev-MS-7B85", 2133384, true},
		{"host-1", "host", 1, true},
		{"host-", "", 0, false},
		{"-42", "", 0, false},
		{"nodash", "", 0, false},
		{"host-abc", "", 0, false},
		// A hostname may itself end with a dash; only the trailing field is a PID.
		{"host--1", "host-", 1, true},
	}
	for _, tc := range tests {
		host, pid, ok := parseSingletonLock(tc.target)
		if ok != tc.wantOK || host != tc.wantHost || pid != tc.wantPID {
			t.Errorf("parseSingletonLock(%q) = (%q, %d, %t), want (%q, %d, %t)",
				tc.target, host, pid, ok, tc.wantHost, tc.wantPID, tc.wantOK)
		}
	}
}
