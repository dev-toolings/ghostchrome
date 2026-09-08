package provider_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/provider"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/defaults"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

func TestLocalLaunchFailureCleansTemporaryProfile(t *testing.T) {
	oldPrefix := launcher.DefaultUserDataDirPrefix
	prefix := filepath.Join(t.TempDir(), "rod-user-data")
	launcher.DefaultUserDataDirPrefix = prefix
	t.Cleanup(func() { launcher.DefaultUserDataDirPrefix = oldPrefix })

	oldBin := defaults.Bin
	defaults.Bin = filepath.Join(t.TempDir(), "missing-chrome")
	t.Cleanup(func() { defaults.Bin = oldBin })
	t.Setenv("GHOSTCHROME_BUNDLED_CHROME", "1")

	p := provider.Local{}
	_, _, err := p.Connect(context.Background(), provider.ConnectOpts{Headless: true})
	if err == nil {
		t.Fatal("expected local provider launch failure")
	}
	if dirs := providerProfileDirs(t, prefix); len(dirs) != 0 {
		t.Fatalf("temporary provider profiles remain after launch failure: %v", dirs)
	}
}

func TestProviderFunctionalMatrix10(t *testing.T) {
	if testing.Short() {
		t.Skip("requires real Chrome provider lifecycle scenarios")
	}
	cases := []string{
		"temporary_cleanup",
		"temporary_double_cleanup",
		"temporary_attach",
		"temporary_page",
		"temporary_browser_death",
		"temporary_unique_profile",
		"explicit_preserved",
		"explicit_attach",
		"explicit_page",
		"explicit_double_cleanup",
	}
	if len(cases) != 10 {
		t.Fatalf("provider functional cases = %d, want 10", len(cases))
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) { runProviderFunctionalCase(t, name) })
	}
}

func runProviderFunctionalCase(t *testing.T, name string) {
	t.Helper()
	old := launcher.DefaultUserDataDirPrefix
	prefix := filepath.Join(t.TempDir(), "rod-user-data")
	launcher.DefaultUserDataDirPrefix = prefix
	t.Cleanup(func() { launcher.DefaultUserDataDirPrefix = old })

	opts := provider.ConnectOpts{Headless: true, TimeoutSec: 10}
	var marker string
	if name == "explicit_preserved" || name == "explicit_attach" || name == "explicit_page" || name == "explicit_double_cleanup" {
		profile := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(profile, 0o700); err != nil {
			t.Fatalf("create profile: %v", err)
		}
		marker = filepath.Join(profile, "preserve-marker")
		if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
			t.Fatalf("write marker: %v", err)
		}
		opts.UserDataDir = profile
		t.Cleanup(func() { time.Sleep(100 * time.Millisecond) })
	}

	p := provider.Local{}
	wsURL, cleanup, err := p.Connect(context.Background(), opts)
	if err != nil {
		t.Fatalf("provider connect: %v", err)
	}
	if name == "temporary_unique_profile" {
		dirs := providerProfileDirs(t, prefix)
		if len(dirs) != 1 || filepath.Dir(dirs[0]) != prefix {
			cleanup()
			t.Fatalf("temporary profile = %v", dirs)
		}
	}
	if name == "temporary_attach" || name == "temporary_page" || name == "temporary_browser_death" || name == "explicit_attach" || name == "explicit_page" {
		b := rod.New().ControlURL(wsURL).Timeout(10 * time.Second)
		if err := b.Connect(); err != nil {
			cleanup()
			t.Fatalf("attach provider Chrome: %v", err)
		}
		if name == "temporary_browser_death" {
			_ = b.Close()
		} else if name == "temporary_page" || name == "explicit_page" {
			page, err := b.Page(proto.TargetCreateTarget{})
			if err != nil {
				cleanup()
				t.Fatalf("provider page: %v", err)
			}
			if _, err := page.Info(); err != nil {
				cleanup()
				t.Fatalf("provider page info: %v", err)
			}
		} else if _, err := b.Version(); err != nil {
			cleanup()
			t.Fatalf("provider browser version: %v", err)
		}
	}

	cleanup()
	if name == "temporary_double_cleanup" || name == "explicit_double_cleanup" {
		cleanup()
	}
	if marker != "" {
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("persistent provider marker removed: %v", err)
		}
	} else if dirs := providerProfileDirs(t, prefix); len(dirs) != 0 {
		t.Fatalf("temporary provider profiles remain: %v", dirs)
	}
}

func TestLocalName(t *testing.T) {
	p := provider.Local{}
	if p.Name() != "local" {
		t.Fatalf("expected 'local', got %q", p.Name())
	}
}

func TestLocalCleanupRemovesTemporaryProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	old := launcher.DefaultUserDataDirPrefix
	prefix := filepath.Join(t.TempDir(), "rod-user-data")
	launcher.DefaultUserDataDirPrefix = prefix
	t.Cleanup(func() { launcher.DefaultUserDataDirPrefix = old })

	p := provider.Local{}
	_, cleanup, err := p.Connect(context.Background(), provider.ConnectOpts{Headless: true, TimeoutSec: 10})
	if err != nil {
		t.Fatalf("connect local provider: %v", err)
	}
	if dirs := providerProfileDirs(t, prefix); len(dirs) != 1 {
		cleanup()
		t.Fatalf("live temporary profiles = %v, want one", dirs)
	}
	cleanup()
	cleanup()
	if dirs := providerProfileDirs(t, prefix); len(dirs) != 0 {
		t.Fatalf("temporary provider profiles remain: %v", dirs)
	}
}

func TestLocalCleanupPreservesExplicitProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	profile := filepath.Join(t.TempDir(), "provider-profile")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	marker := filepath.Join(profile, "preserve-marker")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	t.Cleanup(func() { time.Sleep(100 * time.Millisecond) })

	p := provider.Local{}
	_, cleanup, err := p.Connect(context.Background(), provider.ConnectOpts{
		Headless:    true,
		TimeoutSec:  10,
		UserDataDir: profile,
	})
	if err != nil {
		t.Fatalf("connect local provider: %v", err)
	}
	cleanup()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("explicit provider profile was not preserved: %v", err)
	}
}

func providerProfileDirs(t *testing.T, prefix string) []string {
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

func TestLocalImplementsProvider(t *testing.T) {
	var _ provider.Provider = provider.Local{}
}
