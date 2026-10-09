//go:build windows

package engine

// pidAlive is conservative on Windows: Chrome's SingletonLock is not a
// symlink there, so the sweep never reaches this, and no profile is removed.
func pidAlive(int) bool { return true }
