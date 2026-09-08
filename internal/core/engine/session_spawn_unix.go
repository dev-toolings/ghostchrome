//go:build !windows

package engine

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// processCmdline returns a process's command line, or false if it can't be
// read. Linux uses /proc; other unixes (macOS) fall back to ps.
func processCmdline(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		return strings.ReplaceAll(string(data), "\x00", " "), true
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", false
	}
	return s, true
}

// detachSysProcAttr starts the child in a new session (setsid) so it survives
// the parent CLI process exiting.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// DetachSysProcAttr returns platform-specific process attributes for a child
// that should survive the parent CLI process.
func DetachSysProcAttr() *syscall.SysProcAttr {
	return detachSysProcAttr()
}

// killPID sends SIGTERM so serve can shut Chrome down cleanly.
func killPID(pid int) error {
	if pid <= 0 {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}

// KillProcess signals a managed background process.
func KillProcess(pid int) error {
	return killPID(pid)
}
