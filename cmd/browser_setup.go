package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/proxy"
	"github.com/go-rod/rod"
)

// skipImplicitDaemon is set by commands that launch their own Chrome (serve)
// to prevent the implicit daemon from spawning a second one.
var skipImplicitDaemon bool

func buildBrowserOpts() engine.BrowserOpts {
	// Named session (`-s` / $PLAYWRIGHT_CLI_SESSION / $GHOSTCHROME_SESSION):
	// resolve to a managed, persistent Chrome. Unlike --connect=auto we do NOT
	// mark the tab fresh — the active tab is reused across calls so state (and
	// @refs) persist.
	// A daemon-launching command (serve) manages its OWN Chrome and must never
	// resolve or acquire a managed session. If it did, an inherited
	// $GHOSTCHROME_SESSION / $PLAYWRIGHT_CLI_SESSION would make each spawned
	// serve acquire another serve — a recursive fork bomb. Gate ALL session
	// resolution (env name, default fallback, and acquire) on !skipImplicitDaemon.
	if flagSession == "" && !skipImplicitDaemon {
		flagSession = sessionNameFromEnv()
	}
	// A connect URL resolved from the session registry points at a Chrome we
	// own, so it is safe (and expected) to replay our own emulation profile on
	// it. A user-supplied --connect is not: it may be their personal browser.
	managedSession := false
	if flagSession == "" && flagConnect == "" && !skipImplicitDaemon {
		if ws, ok := engine.DefaultSession(); ok {
			flagConnect = ws
			managedSession = true
			fmt.Fprintf(os.Stderr, "[session %s] %s\n", engine.DefaultSessionName, ws)
		} else if implicitSessionEnabled() {
			flagSession = engine.DefaultSessionName
		}
	}
	if flagSession != "" && flagConnect == "" && !skipImplicitDaemon {
		ws, err := engine.AcquireSession(flagSession, engine.SessionSpawnOpts{
			Headless:       flagHeadless,
			Stealth:        flagStealth,
			Proxy:          flagProxy,
			ProxyBypass:    flagProxyBypass,
			ExecutablePath: flagConfigExecutablePath,
			ConfigPath:     flagConfig,
		})
		if err != nil {
			exitErr("session", err)
		}
		fmt.Fprintf(os.Stderr, "[session %s] %s\n", flagSession, ws)
		flagConnect = ws
		managedSession = true
	}

	connectURL := flagConnect
	attachFresh := false
	if connectURL == "auto" {
		ws, err := engine.DiscoverCDP(nil, 800*time.Millisecond)
		if err != nil {
			exitErr("connect=auto", fmt.Errorf("%w (start Chrome with --remote-debugging-port=9222)", err))
		}
		fmt.Fprintf(os.Stderr, "[connect=auto] attached to %s\n", ws)
		connectURL = ws
		attachFresh = true
	}
	opts := engine.BrowserOpts{
		ConnectURL:     connectURL,
		Headless:       flagHeadless,
		Invisible:      flagInvisible,
		TimeoutSec:     flagTimeout,
		CDPHeaders:     flagConfigCDPHeaders,
		CDPTimeoutMS:   flagConfigCDPTimeoutMS,
		Proxy:          flagProxy,
		ProxyBypass:    flagProxyBypass,
		ExecutablePath: flagConfigExecutablePath,
		LaunchArgs:     flagConfigLaunchArgs,
		AttachFresh:    attachFresh,
		ContextName:    flagContext,
		ManagedSession: managedSession,
	}
	if flagTab >= 0 {
		if connectURL == "" {
			fmt.Fprintln(os.Stderr, "warning: --tab is ignored without --connect (auto-launch always uses a fresh tab)")
		} else {
			opts.TargetTabIndex = &flagTab
		}
	}
	if connectURL == "" && flagContext != "" {
		fmt.Fprintln(os.Stderr, "warning: --context ignored without --connect (auto-launch already has its own isolated profile)")
		opts.ContextName = ""
	}
	if connectURL != "" {
		if flagUserProfile != "" {
			fmt.Fprintln(os.Stderr, "warning: --user-profile ignored when --connect is set (cannot change Chrome user_data_dir of a running instance)")
		}
		if flagUserDataDir != "" {
			fmt.Fprintln(os.Stderr, "warning: --profile / config browser.userDataDir ignored when --connect is set (cannot change Chrome user_data_dir of a running instance)")
		}
		if flagProxy != "" {
			fmt.Fprintln(os.Stderr, "warning: --proxy ignored when --connect is set (proxy is fixed at Chrome launch time)")
			opts.Proxy = ""
		}
		if flagProxyBypass != "" {
			fmt.Fprintln(os.Stderr, "warning: --proxy-bypass / config proxy.bypass ignored when --connect is set (proxy is fixed at Chrome launch time)")
			opts.ProxyBypass = ""
		}
		if flagConfigExecutablePath != "" {
			fmt.Fprintln(os.Stderr, "warning: config executablePath / PLAYWRIGHT_MCP_EXECUTABLE_PATH ignored when --connect is set (browser binary is fixed at Chrome launch time)")
			opts.ExecutablePath = ""
		}
		if len(flagConfigLaunchArgs) > 0 {
			fmt.Fprintln(os.Stderr, "warning: config launchOptions.args ignored when --connect is set (browser flags are fixed at Chrome launch time)")
			opts.LaunchArgs = nil
		}
		if flagDefaultExtensions || flagExtensions != "" {
			fmt.Fprintln(os.Stderr, "warning: --extensions / --default-extensions ignored when --connect is set (extensions are loaded at Chrome launch time)")
		}
		return opts
	}
	if flagUserDataDir != "" {
		dir := flagUserDataDir
		if !filepath.IsAbs(dir) {
			abs, err := filepath.Abs(dir)
			if err != nil {
				exitErr("profile", err)
			}
			dir = abs
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			exitErr("profile", err)
		}
		opts.UserDataDir = dir
	}
	if flagUserProfile != "" {
		dir, err := engine.ResolveProfileDir(flagUserProfile)
		if err != nil {
			exitErr("user-profile", err)
		}
		opts.UserDataDir = dir
	}
	if flagDefaultExtensions {
		paths, err := engine.DefaultExtensionPaths()
		if err != nil {
			exitErr("default-extensions", err)
		}
		opts.Extensions = append(opts.Extensions, paths...)
	}
	if flagExtensions != "" {
		for _, p := range strings.Split(flagExtensions, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if !filepath.IsAbs(p) {
				exitErr("extensions", fmt.Errorf("extension path must be absolute: %q", p))
			}
			manifest := filepath.Join(p, "manifest.json")
			if _, err := os.Stat(manifest); err != nil {
				fmt.Fprintf(os.Stderr, "warning: extension %q skipped (missing manifest.json)\n", p)
				continue
			}
			opts.Extensions = append(opts.Extensions, p)
		}
	}
	return opts
}

func openPage() (*engine.Browser, *rod.Page) {
	opts := buildBrowserOpts()
	b, err := engine.NewBrowserWith(opts)
	if err != nil {
		exitErr("browser", err)
	}
	// Callers still `defer b.Close()` for the success path; this covers the
	// exit paths, which skip defers. Close is closeOnce-guarded, so whichever
	// fires second is a no-op.
	registerCleanup(b.Close)

	page, err := b.Page()
	// A freshly spawned session Chrome can still be churning targets when we
	// attach; "Session with given id not found" (-32001) at this point is
	// transient — retry briefly before giving up.
	for attempt := 0; attempt < 4 && err != nil && isTransientTargetErr(err); attempt++ {
		time.Sleep(300 * time.Millisecond)
		page, err = b.Page()
	}
	if err != nil {
		b.Close()
		exitErr("page", err)
	}
	applyConfigBrowserOptions(b)

	if opts.UserDataDir != "" && opts.ConnectURL == "" {
		if cookies, cerr := engine.LoadCookiesJSON(opts.UserDataDir); cerr == nil && len(cookies) > 0 {
			if injected, ierr := engine.InjectCookies(page, cookies); ierr != nil {
				fmt.Fprintf(os.Stderr, "warning: cookie injection failed: %v\n", ierr)
			} else {
				fmt.Fprintf(os.Stderr, "[cookies] injected %d/%d from imported profile\n", injected, len(cookies))
			}
		}
	}
	applyConfigContextOptions(page)
	applyConfigInitScripts(page)
	applyConfigServiceWorkers(page)
	applyConfigPermissions(b)
	applyConfigStorageState(b, page)
	autoStartVideoIfConfigured(b)
	if flagSession != "" {
		engine.TouchSessionLease(flagSession)
	}
	if flagStealth {
		// The background observer subscribes to Runtime.consoleAPICalled /
		// Runtime.exceptionThrown, which makes rod auto-enable the Runtime
		// CDP domain on every command — a persistent, detectable signal in
		// stealth mode. Skip it; console/network capture stays opt-in via
		// the explicit `errors`/`preview`/`recorder` commands.
		fmt.Fprintln(os.Stderr, "ghostchrome: console capture disabled in stealth (avoids Runtime.enable)")
	} else {
		b.StartBackgroundObserver(page)
	}

	engine.StartFileChooserIntercept(page)

	return b, page
}

func applyConfigContextOptions(page *rod.Page) {
	if flagConfigDevice != "" {
		device, ok := engine.DeviceByName(flagConfigDevice)
		if !ok {
			fmt.Fprintf(os.Stderr, "warning: config device not applied: unknown device %q\n", flagConfigDevice)
		} else if err := engine.ApplyDevice(page, device); err != nil {
			fmt.Fprintf(os.Stderr, "warning: config device not applied: %v\n", err)
		}
	}
	if flagConfigViewportW > 0 && flagConfigViewportH > 0 {
		if err := engine.SetViewport(page, flagConfigViewportW, flagConfigViewportH); err != nil {
			fmt.Fprintf(os.Stderr, "warning: config viewport not applied: %v\n", err)
		}
	}
	if flagConfigUserAgent != "" {
		if err := engine.ApplyUserAgentLocale(page, flagConfigUserAgent, flagConfigLocale); err != nil {
			fmt.Fprintf(os.Stderr, "warning: config userAgent not applied: %v\n", err)
		}
	} else if flagConfigLocale != "" {
		if err := engine.ApplyLocale(page, flagConfigLocale); err != nil {
			fmt.Fprintf(os.Stderr, "warning: config locale not applied: %v\n", err)
		}
	}
}

func applyConfigBrowserOptions(b *engine.Browser) {
	if !flagConfigIgnoreHTTPSErr || b == nil {
		return
	}
	if err := b.RodBrowser().IgnoreCertErrors(true); err != nil {
		fmt.Fprintf(os.Stderr, "warning: config ignore HTTPS errors not applied: %v\n", err)
	}
}

func applyConfigStorageState(b *engine.Browser, page *rod.Page) {
	if flagConfigStorageState == "" || b == nil || page == nil {
		return
	}
	state, err := readStorageStateFile(flagConfigStorageState)
	if err != nil {
		exitErr("config storageState", err)
	}
	if err := engine.LoadStorageState(b.RodBrowser(), page, state); err != nil {
		exitErr("config storageState", err)
	}
}

func applyConfigPermissions(b *engine.Browser) {
	if len(flagConfigPermissions) == 0 || b == nil {
		return
	}
	if err := engine.GrantPlaywrightPermissions(b.RodBrowser(), flagConfigPermissions); err != nil {
		fmt.Fprintf(os.Stderr, "warning: config permissions not applied: %v\n", err)
	}
}

func applyConfigServiceWorkers(page *rod.Page) {
	if flagConfigServiceWorkers == "" || page == nil {
		return
	}
	if err := engine.ApplyServiceWorkersMode(page, flagConfigServiceWorkers); err != nil {
		fmt.Fprintf(os.Stderr, "warning: config serviceWorkers not applied: %v\n", err)
	}
}

func applyConfigInitScripts(page *rod.Page) {
	if len(flagConfigInitScripts) == 0 || page == nil {
		return
	}
	if err := engine.ApplyInitScriptFiles(page, flagConfigInitScripts); err != nil {
		exitErr("config initScript", err)
	}
}

// isTransientTargetErr reports whether the error is a CDP target/session
// race that resolves itself once the freshly spawned Chrome settles.
func isTransientTargetErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Session with given id not found") ||
		strings.Contains(msg, "No target with given id") ||
		strings.Contains(msg, "-32001")
}

func applyStealthIfNeeded(page *rod.Page) {
	if flagStealth {
		// Stealth is best-effort hardening, not correctness. Give it its own
		// fresh, bounded context so a slow/heavy page (or an already-exhausted
		// command timeout) can't cancel it, and NEVER abort the command if a
		// patch fails — just warn. (Sessions are already stealthed at spawn;
		// this re-arms the active page's next navigation.)
		sctx, scancel := context.WithTimeout(context.Background(), 8*time.Second)
		if err := engine.ApplyStealth(page.Context(sctx)); err != nil {
			fmt.Fprintf(os.Stderr, "warning: stealth not fully applied: %v\n", err)
		}
		scancel()
	}
	if flagProxy != "" && flagConnect == "" {
		if err := proxy.ApplyProxyAuth(page, flagProxy); err != nil {
			exitErr("proxy-auth", err)
		}
	}
}

func dismissCookiesIfNeeded(page *rod.Page) {
	if flagDismissCookies && engine.DismissCookieBanner(page) {
		_ = engine.WaitForPage(page, "stable")
	}
}
