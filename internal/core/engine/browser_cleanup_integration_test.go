package engine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/defaults"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

func TestBrowserCloseRemovesOwnedRodUserDataDir(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	prefix := useIsolatedRodUserDataPrefix(t)
	b, err := NewBrowserWith(BrowserOpts{Headless: true, TimeoutSec: 10})
	if err != nil {
		t.Fatalf("launch browser: %v", err)
	}

	profiles := rodProfileDirs(t, prefix)
	if len(profiles) != 1 {
		b.Close()
		t.Fatalf("expected one live Rod profile, got %d: %v", len(profiles), profiles)
	}
	if _, err := os.Stat(profiles[0]); err != nil {
		b.Close()
		t.Fatalf("stat live Rod profile: %v", err)
	}

	b.Close()
	if _, err := os.Stat(profiles[0]); !os.IsNotExist(err) {
		t.Fatalf("owned Rod profile still exists after Close: %s (stat error: %v)", profiles[0], err)
	}
}

func TestLauncherUsesRodTempProfileRequiresGeneratedFinalPath(t *testing.T) {
	useIsolatedRodUserDataPrefix(t)

	generated := NewLauncher(LauncherOpts{Headless: true})
	if !LauncherOwnsRodTempProfile(generated, "", nil) {
		t.Fatal("expected Rod-generated final user data dir to be owned")
	}

	foreign := NewLauncher(LauncherOpts{Headless: true}).UserDataDir(filepath.Join(t.TempDir(), "foreign-profile"))
	if LauncherOwnsRodTempProfile(foreign, "", nil) {
		t.Fatal("final user data dir outside the Rod prefix must not be owned")
	}
}

func TestLauncherUsesRodTempProfilePreservesDefaultsDir(t *testing.T) {
	prefix := useIsolatedRodUserDataPrefix(t)
	oldDir := defaults.Dir
	defaults.Dir = filepath.Join(prefix, "persistent-default")
	t.Cleanup(func() {
		defaults.Dir = oldDir
	})

	l := NewLauncher(LauncherOpts{Headless: true})
	if LauncherOwnsRodTempProfile(l, "", nil) {
		t.Fatal("Rod defaults.Dir must never be owned, even as a direct child of the temporary prefix")
	}
}

func TestNewBrowserWithCleansOwnedProfileAfterPostLaunchCDPFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	prefix := useIsolatedRodUserDataPrefix(t)
	wantErr := errors.New("injected post-launch CDP failure")
	originalConnect := connectLaunchedRodBrowser
	connectLaunchedRodBrowser = func(string, time.Duration, int, map[string]string) (*rod.Browser, error) {
		return nil, wantErr
	}
	t.Cleanup(func() {
		connectLaunchedRodBrowser = originalConnect
	})

	b, err := NewBrowserWith(BrowserOpts{Headless: true, TimeoutSec: 10})
	if b != nil {
		b.Close()
		t.Fatal("expected no Browser after injected CDP failure")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected injected CDP failure, got %v", err)
	}
	if profiles := rodProfileDirs(t, prefix); len(profiles) != 0 {
		t.Fatalf("Rod profiles remain after post-launch CDP failure: %v", profiles)
	}
}

func TestBrowserCloseDoesNotOwnExternalConnectURLProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	profile := filepath.Join(t.TempDir(), "external-profile")
	marker := createProfileMarker(t, profile)
	_, controlURL := launchExplicitProfileChrome(t, profile)
	b, err := NewBrowserWith(BrowserOpts{ConnectURL: controlURL, Headless: true, TimeoutSec: 10})
	if err != nil {
		t.Fatalf("connect to external Chrome: %v", err)
	}

	b.Close()
	if !b.Alive(time.Second) {
		t.Fatal("external Chrome stopped after Browser.Close")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("external ConnectURL profile was not preserved: %v", err)
	}
}

func TestBrowserClosePreservesProviderProfileAndRunsCleanupOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	profile := filepath.Join(t.TempDir(), "provider-profile")
	marker := createProfileMarker(t, profile)
	l, controlURL := launchExplicitProfileChrome(t, profile)
	var cleanupCalls atomic.Int32
	b, err := NewBrowserWith(BrowserOpts{
		Headless:   true,
		TimeoutSec: 10,
		ProviderFunc: func(context.Context) (string, func(), error) {
			return controlURL, func() {
				cleanupCalls.Add(1)
				l.Kill()
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("connect to provider Chrome: %v", err)
	}

	b.Close()
	b.Close()
	if got := cleanupCalls.Load(); got != 1 {
		t.Fatalf("expected provider cleanup once, got %d", got)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("provider profile was not preserved: %v", err)
	}
}

func TestBrowserClosePreservesExplicitUserDataDir(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	profile := filepath.Join(t.TempDir(), "explicit-profile")
	marker := createProfileMarker(t, profile)
	b, err := NewBrowserWith(BrowserOpts{
		Headless:    true,
		TimeoutSec:  10,
		UserDataDir: profile,
	})
	if err != nil {
		t.Fatalf("launch browser with explicit profile: %v", err)
	}
	b.Close()

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("explicit user data dir was not preserved: %v", err)
	}
}

func TestBrowserClosePreservesLaunchArgUserDataDir(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	profile := filepath.Join(t.TempDir(), "launch-arg-profile")
	marker := createProfileMarker(t, profile)
	b, err := NewBrowserWith(BrowserOpts{
		Headless:   true,
		TimeoutSec: 10,
		LaunchArgs: []string{"--user-data-dir=" + profile},
	})
	if err != nil {
		t.Fatalf("launch browser with --user-data-dir: %v", err)
	}
	b.Close()

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("launch-arg user data dir was not preserved: %v", err)
	}
}

func TestBrowserCloseIsIdempotentAndConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	prefix := useIsolatedRodUserDataPrefix(t)
	b, err := NewBrowserWith(BrowserOpts{Headless: true, TimeoutSec: 10})
	if err != nil {
		t.Fatalf("launch browser: %v", err)
	}
	profiles := rodProfileDirs(t, prefix)
	if len(profiles) != 1 {
		b.Close()
		t.Fatalf("expected one live Rod profile, got %d: %v", len(profiles), profiles)
	}

	const callers = 32
	closed := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		wg.Add(callers)
		for range callers {
			go func() {
				defer wg.Done()
				b.Close()
			}()
		}
		wg.Wait()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent Close calls did not return")
	}
	b.Close()

	if _, err := os.Stat(profiles[0]); !os.IsNotExist(err) {
		t.Fatalf("owned Rod profile still exists after concurrent Close: %s (stat error: %v)", profiles[0], err)
	}
}

func TestBrowserLifecycleFunctionalMatrix70(t *testing.T) {
	if testing.Short() {
		t.Skip("requires 70 real Chrome lifecycle scenarios")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><title>matrix</title><button>ok</button>`))
	}))
	defer server.Close()
	prefix := useIsolatedRodUserDataPrefix(t)
	modes := []string{"rod_temp", "explicit_option", "explicit_launch_arg", "external_connect", "provider", "concurrent_close", "crash_then_close"}
	actions := browserFunctionalActions(server.URL)
	if got := len(modes) * len(actions); got != 70 {
		t.Fatalf("functional matrix has %d cases, want 70", got)
	}
	for _, mode := range modes {
		for _, action := range actions {
			t.Run(mode+"/"+action.name, func(t *testing.T) {
				runBrowserFunctionalCase(t, prefix, mode, action)
			})
		}
	}
}

type browserFunctionalAction struct {
	name string
	run  func(*Browser) error
}

func browserFunctionalActions(serverURL string) []browserFunctionalAction {
	page := func(b *Browser) (*rod.Page, error) { return b.Page() }
	return []browserFunctionalAction{
		{name: "browser_alive", run: func(b *Browser) error {
			if !b.Alive(time.Second) {
				return errors.New("browser is not alive")
			}
			return nil
		}},
		{name: "page_info", run: func(b *Browser) error {
			p, err := page(b)
			if err != nil {
				return err
			}
			_, err = p.Info()
			return err
		}},
		{name: "navigate_about_blank", run: func(b *Browser) error {
			p, err := page(b)
			if err != nil {
				return err
			}
			_, err = Navigate(p, "about:blank", "load")
			return err
		}},
		{name: "navigate_local_http", run: func(b *Browser) error {
			p, err := page(b)
			if err != nil {
				return err
			}
			_, err = Navigate(p, serverURL, "load")
			return err
		}},
		{name: "eval_document_title", run: func(b *Browser) error {
			p, err := page(b)
			if err != nil {
				return err
			}
			if _, err = Navigate(p, serverURL, "load"); err != nil {
				return err
			}
			_, err = p.Eval(`() => document.title`)
			return err
		}},
		{name: "create_second_page", run: func(b *Browser) error {
			p, err := b.RodBrowser().Page(proto.TargetCreateTarget{})
			if err == nil {
				err = p.Close()
			}
			return err
		}},
		{name: "extract_skeleton", run: func(b *Browser) error {
			p, err := page(b)
			if err != nil {
				return err
			}
			if _, err = Navigate(p, serverURL, "load"); err != nil {
				return err
			}
			_, err = Extract(p, LevelSkeleton, "", false)
			return err
		}},
		{name: "set_current_second_page", run: func(b *Browser) error {
			p, err := b.RodBrowser().Page(proto.TargetCreateTarget{})
			if err != nil {
				return err
			}
			return b.SetCurrentPage(p)
		}},
		{name: "set_viewport", run: func(b *Browser) error {
			p, err := page(b)
			if err != nil {
				return err
			}
			return SetViewport(p, 1280, 720)
		}},
		{name: "navigation_error_observed", run: func(b *Browser) error {
			p, err := page(b)
			if err != nil {
				return err
			}
			if _, err = Navigate(p, "http://127.0.0.1:1", "load"); err == nil {
				return errors.New("expected navigation error")
			}
			return nil
		}},
	}
}

func runBrowserFunctionalCase(t *testing.T, prefix, mode string, action browserFunctionalAction) {
	t.Helper()
	var b *Browser
	var marker string
	var externalLauncher *launcher.Launcher
	var providerCleanupCalls atomic.Int32
	var err error

	switch mode {
	case "rod_temp", "concurrent_close", "crash_then_close":
		b, err = NewBrowserWith(BrowserOpts{Headless: true, TimeoutSec: 10})
	case "explicit_option", "explicit_launch_arg", "external_connect", "provider":
		profile := filepath.Join(t.TempDir(), mode+"-profile")
		marker = createProfileMarker(t, profile)
		switch mode {
		case "explicit_option":
			b, err = NewBrowserWith(BrowserOpts{Headless: true, TimeoutSec: 10, UserDataDir: profile})
		case "explicit_launch_arg":
			b, err = NewBrowserWith(BrowserOpts{Headless: true, TimeoutSec: 10, LaunchArgs: []string{"--user-data-dir=" + profile}})
		case "external_connect", "provider":
			var controlURL string
			externalLauncher, controlURL = launchExplicitProfileChrome(t, profile)
			if mode == "external_connect" {
				b, err = NewBrowserWith(BrowserOpts{ConnectURL: controlURL, TimeoutSec: 10})
			} else {
				b, err = NewBrowserWith(BrowserOpts{Headless: true, TimeoutSec: 10, ProviderFunc: func(context.Context) (string, func(), error) {
					return controlURL, func() {
						providerCleanupCalls.Add(1)
						externalLauncher.Kill()
					}, nil
				}})
			}
		}
	default:
		t.Fatalf("unknown lifecycle mode %q", mode)
	}
	if err != nil {
		t.Fatalf("launch %s: %v", mode, err)
	}
	if err := action.run(b); err != nil {
		b.Close()
		t.Fatalf("%s: %v", action.name, err)
	}

	switch mode {
	case "concurrent_close":
		var wg sync.WaitGroup
		wg.Add(8)
		for range 8 {
			go func() { defer wg.Done(); b.Close() }()
		}
		wg.Wait()
	case "crash_then_close":
		_ = b.RodBrowser().Close()
		b.Close()
	default:
		b.Close()
	}

	if mode == "rod_temp" || mode == "concurrent_close" || mode == "crash_then_close" {
		if profiles := rodProfileDirs(t, prefix); len(profiles) != 0 {
			t.Fatalf("%s left temporary profiles: %v", mode, profiles)
		}
	} else if _, err := os.Stat(marker); err != nil {
		t.Fatalf("%s removed explicit profile marker: %v", mode, err)
	}
	if mode == "external_connect" {
		if !b.Alive(time.Second) {
			t.Fatal("external browser was closed")
		}
		externalLauncher.Kill()
	}
	if mode == "provider" && providerCleanupCalls.Load() != 1 {
		t.Fatalf("provider cleanup calls = %d, want 1", providerCleanupCalls.Load())
	}
}

func launchExplicitProfileChrome(t *testing.T, profile string) (*launcher.Launcher, string) {
	t.Helper()

	l := NewLauncher(LauncherOpts{Headless: true, UserDataDir: profile})
	controlURL, err := l.Launch()
	if err != nil {
		t.Fatalf("launch explicit-profile Chrome: %v", err)
	}
	t.Cleanup(l.Kill)
	return l, controlURL
}

func useIsolatedRodUserDataPrefix(t *testing.T) string {
	t.Helper()

	oldPrefix := launcher.DefaultUserDataDirPrefix
	prefix := filepath.Join(t.TempDir(), "rod-user-data")
	launcher.DefaultUserDataDirPrefix = prefix
	t.Cleanup(func() {
		launcher.DefaultUserDataDirPrefix = oldPrefix
	})
	return prefix
}

func createProfileMarker(t *testing.T, profile string) string {
	t.Helper()

	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatalf("create explicit profile: %v", err)
	}
	marker := filepath.Join(profile, "ghostchrome-preserve-marker")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("create explicit profile marker: %v", err)
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(5 * time.Second)
		for {
			err := os.RemoveAll(profile)
			if err == nil {
				time.Sleep(100 * time.Millisecond)
				if _, statErr := os.Stat(profile); os.IsNotExist(statErr) {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Errorf("remove explicit test profile %s after Chrome exit: %v", profile, err)
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	})
	return marker
}

func rodProfileDirs(t *testing.T, prefix string) []string {
	t.Helper()

	entries, err := os.ReadDir(prefix)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read Rod profile prefix %s: %v", prefix, err)
	}

	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(prefix, entry.Name()))
		}
	}
	return dirs
}
