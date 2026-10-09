package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-rod/rod/lib/launcher"
)

// rodTempProfileName matches the directory names Rod generates under
// launcher.DefaultUserDataDirPrefix (utils.RandString(8): 16 hex chars).
var rodTempProfileName = regexp.MustCompile(`^[0-9a-f]{16}$`)

// SweepDeadRodProfiles removes Rod temporary profiles left behind by a Chrome
// that no longer runs. Graceful shutdown already deletes them; a SIGKILLed
// owner (OOM, a client killing its MCP server) cannot, and leakless only kills
// Chrome. Each leak costs 3 to 35 MB of /tmp, often a RAM-backed tmpfs.
//
// A profile is removed only when its Chrome SingletonLock names this host and
// a PID that is gone. Profiles without a lock (Chrome still starting, or a
// clean exit) and every non-Rod name are left alone. Returns the count removed.
func SweepDeadRodProfiles() int {
	host, err := os.Hostname()
	if err != nil {
		return 0
	}
	return sweepDeadRodProfiles(launcher.DefaultUserDataDirPrefix, host, pidAlive)
}

func sweepDeadRodProfiles(prefix, host string, alive func(int) bool) int {
	entries, err := os.ReadDir(prefix)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		if !e.IsDir() || !rodTempProfileName.MatchString(e.Name()) {
			continue
		}
		dir := filepath.Join(prefix, e.Name())
		target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
		if err != nil {
			continue
		}
		cut := strings.LastIndexByte(target, '-')
		if cut <= 0 || target[:cut] != host {
			continue
		}
		pid, err := strconv.Atoi(target[cut+1:])
		if err != nil || pid <= 0 || alive(pid) {
			continue
		}
		if os.RemoveAll(dir) == nil {
			removed++
		}
	}
	return removed
}
