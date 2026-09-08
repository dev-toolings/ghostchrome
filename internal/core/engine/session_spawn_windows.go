//go:build windows

package engine

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// processCmdline returns a process's command line via a PowerShell CIM query
// (wmic is deprecated on modern Windows), or false if it can't be read.
func processCmdline(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	query := fmt.Sprintf("(Get-CimInstance Win32_Process -Filter 'ProcessId=%d').CommandLine", pid)
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", query).Output()
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", false
	}
	return s, true
}

// detachSysProcAttr starts the child in a new process group so it is not tied
// to the parent CLI's console.
func detachSysProcAttr() *syscall.SysProcAttr {
	const createNewProcessGroup = 0x00000200
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// DetachSysProcAttr returns platform-specific process attributes for a child
// that should survive the parent CLI process.
func DetachSysProcAttr() *syscall.SysProcAttr {
	return detachSysProcAttr()
}

// killPID terminates the process (Windows has no SIGTERM).
func killPID(pid int) error {
	if pid <= 0 {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// KillProcess terminates a managed background process.
func KillProcess(pid int) error {
	return killPID(pid)
}
