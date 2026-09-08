package engine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// clearStaleProfileLock removes a Chrome SingletonLock left behind by a Chrome
// that was killed instead of closed.
//
// Chrome refuses to start on a profile whose SingletonLock exists ("Failed to
// create SingletonLock: File exists" then "Aborting now to avoid profile
// corruption"), and it only cleans the lock up on a graceful exit. A killed
// daemon — `sessions stop`, a crash, a reboot leaving the file behind — would
// therefore break every later command on that session until someone deleted
// the file by hand.
//
// The lock is a symlink named "<hostname>-<pid>". It is only removed when that
// PID is gone: a live owner keeps its lock, and a lock written by another
// machine (shared home directory) is never touched, because a PID number means
// nothing there.
//
// The owner is given a grace period to disappear. A session that was just
// stopped is the common case here: the previous Chrome got its SIGTERM
// milliseconds ago and is still winding down, so a single liveness check would
// see it alive, keep the lock, and the replacement Chrome would abort on it.
const profileLockGrace = 3 * time.Second

func clearStaleProfileLock(profileDir string) {
	clearProfileLockWithin(profileDir, profileLockGrace)
}

func clearProfileLockWithin(profileDir string, grace time.Duration) {
	if profileDir == "" {
		return
	}
	lock := filepath.Join(profileDir, "SingletonLock")
	deadline := time.Now().Add(grace)
	for {
		target, err := os.Readlink(lock)
		if err != nil {
			return // no lock, or not a symlink we understand
		}
		host, pid, ok := parseSingletonLock(target)
		if !ok {
			return
		}
		if local, err := os.Hostname(); err == nil && host != "" && host != local {
			return // another machine owns this profile
		}
		if _, alive := processCmdline(pid); !alive {
			_ = os.Remove(lock)
			return
		}
		if time.Now().After(deadline) {
			return // still owned by a live process — not ours to remove
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// parseSingletonLock splits Chrome's "<hostname>-<pid>" lock target. The
// hostname itself may contain dashes, so the PID is taken after the last one.
func parseSingletonLock(target string) (host string, pid int, ok bool) {
	idx := strings.LastIndexByte(target, '-')
	if idx <= 0 || idx == len(target)-1 {
		return "", 0, false
	}
	pid, err := strconv.Atoi(target[idx+1:])
	if err != nil || pid <= 0 {
		return "", 0, false
	}
	return target[:idx], pid, true
}
