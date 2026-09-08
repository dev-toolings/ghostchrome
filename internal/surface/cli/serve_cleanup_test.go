package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

func TestWarmUpServeChromeFailureCleansTemporaryProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	prefix := isolateServeRodPrefix(t)
	wsURL, cleanup, err := launchServeChrome(engine.LauncherOpts{Headless: true})
	if err != nil {
		t.Fatalf("launch serve Chrome: %v", err)
	}
	originalWarmup := serveStealthWarmup
	serveStealthWarmup = func(string) error { return errors.New("injected stealth failure") }
	t.Cleanup(func() { serveStealthWarmup = originalWarmup })

	if err := warmUpServeChrome(wsURL, cleanup); err == nil {
		t.Fatal("expected injected stealth warmup failure")
	}
	if dirs := serveProfileDirs(t, prefix); len(dirs) != 0 {
		t.Fatalf("temporary serve profiles remain after warmup failure: %v", dirs)
	}
}

func TestLaunchServeChromeFailureCleansTemporaryProfile(t *testing.T) {
	prefix := isolateServeRodPrefix(t)
	_, _, err := launchServeChrome(engine.LauncherOpts{
		Headless:       true,
		ExecutablePath: filepath.Join(t.TempDir(), "missing-chrome"),
	})
	if err == nil {
		t.Fatal("expected launcher failure")
	}
	if dirs := serveProfileDirs(t, prefix); len(dirs) != 0 {
		t.Fatalf("temporary serve profiles remain after launch failure: %v", dirs)
	}
}

func TestServeFunctionalMatrix10(t *testing.T) {
	if testing.Short() {
		t.Skip("requires real Chrome serve lifecycle scenarios")
	}
	cases := []string{
		"temporary_cleanup",
		"temporary_double_cleanup",
		"temporary_attach_page",
		"temporary_second_page",
		"temporary_browser_death",
		"temporary_unique_profile",
		"explicit_option_preserved",
		"explicit_option_attach_page",
		"launch_arg_preserved",
		"launch_arg_attach_page",
	}
	if len(cases) != 10 {
		t.Fatalf("serve functional cases = %d, want 10", len(cases))
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) { runServeFunctionalCase(t, name) })
	}
}

func runServeFunctionalCase(t *testing.T, name string) {
	t.Helper()
	prefix := isolateServeRodPrefix(t)
	opts := engine.LauncherOpts{Headless: true}
	var marker string
	if name == "explicit_option_preserved" || name == "explicit_option_attach_page" {
		profile := filepath.Join(t.TempDir(), name)
		marker = serveProfileMarker(t, profile)
		opts.UserDataDir = profile
	}
	if name == "launch_arg_preserved" || name == "launch_arg_attach_page" {
		profile := filepath.Join(t.TempDir(), name)
		marker = serveProfileMarker(t, profile)
		opts.Args = []string{"--user-data-dir=" + profile}
	}

	wsURL, cleanup, err := launchServeChrome(opts)
	if err != nil {
		t.Fatalf("launch serve Chrome: %v", err)
	}
	if name == "temporary_unique_profile" {
		dirs := serveProfileDirs(t, prefix)
		if len(dirs) != 1 || filepath.Dir(dirs[0]) != prefix {
			cleanup()
			t.Fatalf("temporary profile = %v", dirs)
		}
	}
	if name == "temporary_attach_page" || name == "temporary_second_page" || name == "explicit_option_attach_page" || name == "launch_arg_attach_page" || name == "temporary_browser_death" {
		b := rod.New().ControlURL(wsURL).Timeout(10 * time.Second)
		if err := b.Connect(); err != nil {
			cleanup()
			t.Fatalf("attach serve Chrome: %v", err)
		}
		if name == "temporary_second_page" {
			p, err := b.Page(proto.TargetCreateTarget{})
			if err != nil {
				cleanup()
				t.Fatalf("second page: %v", err)
			}
			_ = p.Close()
		} else if name == "temporary_browser_death" {
			_ = b.Close()
		} else {
			p, err := b.Page(proto.TargetCreateTarget{})
			if err != nil {
				cleanup()
				t.Fatalf("page: %v", err)
			}
			if _, err := p.Info(); err != nil {
				cleanup()
				t.Fatalf("page info: %v", err)
			}
		}
	}

	cleanup()
	if name == "temporary_double_cleanup" {
		cleanup()
	}
	if marker != "" {
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("persistent serve marker removed: %v", err)
		}
	} else if dirs := serveProfileDirs(t, prefix); len(dirs) != 0 {
		t.Fatalf("temporary serve profiles remain: %v", dirs)
	}
}

func TestLaunchServeChromeRemovesTemporaryProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	prefix := isolateServeRodPrefix(t)
	_, cleanup, err := launchServeChrome(engine.LauncherOpts{Headless: true})
	if err != nil {
		t.Fatalf("launch serve Chrome: %v", err)
	}
	profiles := serveProfileDirs(t, prefix)
	if len(profiles) != 1 {
		cleanup()
		t.Fatalf("live temporary profiles = %v, want one", profiles)
	}

	cleanup()
	cleanup()
	if profiles := serveProfileDirs(t, prefix); len(profiles) != 0 {
		t.Fatalf("temporary serve profiles remain after cleanup: %v", profiles)
	}
}

func TestLaunchServeChromePreservesExplicitProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	profile := filepath.Join(t.TempDir(), "serve-profile")
	marker := serveProfileMarker(t, profile)
	_, cleanup, err := launchServeChrome(engine.LauncherOpts{Headless: true, UserDataDir: profile})
	if err != nil {
		t.Fatalf("launch serve Chrome: %v", err)
	}
	cleanup()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("explicit serve profile was not preserved: %v", err)
	}
}

func TestLaunchServeChromePreservesLaunchArgProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	profile := filepath.Join(t.TempDir(), "serve-launch-arg-profile")
	marker := serveProfileMarker(t, profile)
	_, cleanup, err := launchServeChrome(engine.LauncherOpts{
		Headless: true,
		Args:     []string{"--user-data-dir=" + profile},
	})
	if err != nil {
		t.Fatalf("launch serve Chrome: %v", err)
	}
	cleanup()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("launch-arg serve profile was not preserved: %v", err)
	}
}

func isolateServeRodPrefix(t *testing.T) string {
	t.Helper()
	old := launcher.DefaultUserDataDirPrefix
	prefix := filepath.Join(t.TempDir(), "rod-user-data")
	launcher.DefaultUserDataDirPrefix = prefix
	t.Cleanup(func() { launcher.DefaultUserDataDirPrefix = old })
	return prefix
}

func serveProfileDirs(t *testing.T, prefix string) []string {
	t.Helper()
	entries, err := os.ReadDir(prefix)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read profile prefix: %v", err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(prefix, entry.Name()))
		}
	}
	return dirs
}

func serveProfileMarker(t *testing.T, profile string) string {
	t.Helper()
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	marker := filepath.Join(profile, "preserve-marker")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	t.Cleanup(func() { time.Sleep(100 * time.Millisecond) })
	return marker
}
