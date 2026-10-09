//go:build !windows

package engine

import (
	"errors"
	"syscall"
)

// pidAlive reports whether a process exists. EPERM means it exists but belongs
// to another user, so it counts as alive.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
