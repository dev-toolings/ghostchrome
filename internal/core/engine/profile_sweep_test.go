package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSweepDeadRodProfilesRemovesOnlyDeadLocalLocks(t *testing.T) {
	prefix := t.TempDir()
	mk := func(name, lock string) string {
		dir := filepath.Join(prefix, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if lock != "" {
			if err := os.Symlink(lock, filepath.Join(dir, "SingletonLock")); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	dead := mk("0123456789abcdef", "my-host-111")
	live := mk("1123456789abcdef", "my-host-222")
	starting := mk("2123456789abcdef", "")
	foreign := mk("3123456789abcdef", "other-host-111")
	named := mk("my-profile", "my-host-111")

	alive := func(pid int) bool { return pid == 222 }
	if n := sweepDeadRodProfiles(prefix, "my-host", alive); n != 1 {
		t.Fatalf("removed %d profiles, want 1", n)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatalf("dead profile still present: %v", err)
	}
	for _, dir := range []string{live, starting, foreign, named} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s should be kept: %v", filepath.Base(dir), err)
		}
	}
}
