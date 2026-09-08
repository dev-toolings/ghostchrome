package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/cdp"
	"github.com/go-rod/rod/lib/defaults"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
	"github.com/go-rod/rod/lib/proto"
)

// LauncherOpts configures a stealth-flavored Chrome launcher.
type LauncherOpts struct {
	Headless   bool
	RemotePort int // 0 = random

	// Invisible forces headful Chrome (real rendering pipeline, harder to
	// fingerprint as a bot) but positions the window far off-screen so it
	// stays out of sight. Overrides Headless when set.
	Invisible bool

	// UserDataDir is the absolute Chrome --user-data-dir path. Empty means
	// ephemeral. Use ResolveProfileDir to convert a short profile name into
	// the canonical path under ~/.ghostchrome/profiles/<name>.
	UserDataDir string

	// Proxy is the upstream proxy URL passed to Chrome via --proxy-server.
	// Examples: "http://user:pass@host:port", "socks5://host:1080".
	// Empty means no proxy.
	Proxy string

	// ProxyBypass is passed to Chrome as --proxy-bypass-list when Proxy is set.
	// It uses Chromium's bypass list syntax, matching Playwright's proxy.bypass.
	ProxyBypass string

	// ExecutablePath forces a specific Chrome/Chromium binary. It maps
	// Playwright's browser.launchOptions.executablePath and overrides
	// ghostchrome's system-Chrome preference.
	ExecutablePath string

	// Args are extra Chromium command-line switches, normalized as --flag or
	// --flag=value. They map Playwright's browser.launchOptions.args.
	Args []string

	// Extensions is a list of absolute paths to unpacked Chrome extensions
	// (each path must contain a manifest.json at its root). When non-empty,
	// Chrome is launched with --load-extension and --disable-extensions-except
	// so only the listed extensions are active. Note: requires HeadlessNew
	// (the modern headless mode); old --headless ignores extensions.
	Extensions []string

	// SystemChrome forces the launcher to use the system's Google Chrome
	// binary (com.google.Chrome bundle) instead of rod's bundled Chromium.
	// Required when reusing a profile imported from the user's real Chrome
	// — only com.google.Chrome can decrypt cookies sealed by macOS Keychain
	// "Chrome Safe Storage". Auto-detected via the .ghostchrome-system-chrome
	// marker file in the profile dir.
	SystemChrome bool
}

// FindSystemChromeBinary returns the absolute path to a real, installed
// Google Chrome / Chromium binary, or "" if none is found. Preferring the
// system Chrome over rod's bundled Chromium matters for anti-bot: the
// bundled build can lag many versions behind, and an outdated Chrome
// version is itself a signal Cloudflare-style challenges score against.
func FindSystemChromeBinary() string {
	candidates := []string{
		// macOS
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Google Chrome Beta.app/Contents/MacOS/Google Chrome Beta",
		"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		// Linux — real Google Chrome first (most "normal" fingerprint),
		// then Chromium variants.
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/opt/google/chrome/chrome",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/snap/bin/chromium",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	// Fall back to a PATH lookup for less common install layouts.
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// ProfileUsesSystemChrome returns true when the profile dir was created by
// `ghostchrome import-profile` and is bound to the system Chrome binary.
// We keep this for backward compatibility but with the cookie decryption
// path it's no longer required — the bundled Chromium can now read the
// imported cookies because they were rewritten as plaintext.
func ProfileUsesSystemChrome(userDataDir string) bool {
	if userDataDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(userDataDir, ".ghostchrome-system-chrome"))
	return err == nil
}

// DefaultExtensionNames lists the bundled extension slugs ghostchrome looks
// for under ~/.ghostchrome/extensions/<name>/ when --default-extensions is
// set. Mirrors browser-use's defaults.
var DefaultExtensionNames = []string{"ublock", "icdc", "force-bg"}

// DefaultExtensionsDir returns the absolute path to the per-user extensions
// directory: ~/.ghostchrome/extensions.
func DefaultExtensionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".ghostchrome", "extensions"), nil
}

// DefaultExtensionPaths returns absolute paths to the bundled extensions
// (uBlock Origin Lite, "I still don't care about cookies", Force Background
// Tab) that exist on disk. Missing entries are silently skipped. When none
// are found a hint is printed to stderr.
func DefaultExtensionPaths() ([]string, error) {
	base, err := DefaultExtensionsDir()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(DefaultExtensionNames))
	for _, name := range DefaultExtensionNames {
		dir := filepath.Join(base, name)
		manifest := filepath.Join(dir, "manifest.json")
		if _, err := os.Stat(manifest); err == nil {
			paths = append(paths, dir)
		}
	}
	if len(paths) == 0 {
		fmt.Fprintf(os.Stderr,
			"warning: no default extensions found under %s — run `ghostchrome extensions install` for setup instructions\n",
			base)
	}
	return paths, nil
}

// validProfileName matches sluggified profile names accepted by
// ResolveProfileDir. Anything else is rejected up-front.
var validProfileName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ResolveProfileDir converts a short profile name into the canonical
// persistent Chrome user_data_dir path under ~/.ghostchrome/profiles/<name>.
// The directory is created with 0700 perms if missing.
func ResolveProfileDir(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if !validProfileName.MatchString(name) {
		return "", fmt.Errorf("invalid profile name %q: use [A-Za-z0-9_-]", name)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, ".ghostchrome", "profiles", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create profile dir %s: %w", dir, err)
	}
	return dir, nil
}

// NewLauncher returns a configured launcher with the shared anti-detection
// flags used by both auto-launch (NewBrowser) and the `serve` command.
// --no-sandbox is auto-enabled when running inside a CI runner (env
// GITHUB_ACTIONS / CI) or as root, because those environments disable the
// Chrome sandbox.
func NewLauncher(opts LauncherOpts) *launcher.Launcher {
	// macOS contract: either fully visible or fully invisible — no flicker.
	// --headless=new registers the app in the Dock during startup, causing
	// a visible bounce/flash. The Invisible off-screen trick is even worse:
	// WindowServer animates window creation before the position is applied.
	// On darwin we collapse both "headless" and "invisible" to the legacy
	// --headless mode, which is 100% UI-silent. Anti-bot fingerprint is
	// slightly worse than --headless=new but the UX contract wins.
	wantInvisible := opts.Headless || opts.Invisible
	useLegacyHeadless := runtime.GOOS == "darwin" && wantInvisible
	headlessNew := opts.Headless && !opts.Invisible && !useLegacyHeadless
	l := launcher.New().
		Headless(useLegacyHeadless).
		HeadlessNew(headlessNew).
		Set("disable-blink-features", "AutomationControlled").
		Set("window-size", "1920,1080").
		// Without password-store=basic, Chrome on Linux queries the secret
		// service (gnome-keyring/kwallet) over D-Bus for os_crypt on first
		// navigation; when no service answers, the call blocks for the full
		// 25 s D-Bus timeout. Playwright sets both flags unconditionally for
		// the same reason.
		Set("password-store", "basic").
		Set("use-mock-keychain").
		Delete("enable-automation").
		// Rod's defaults disable site isolation (disable-site-isolation-trials,
		// the "site-per-process" feature) and force the network service
		// in-process (NetworkServiceInProcess) — a real Chrome runs with site
		// isolation ON and the network service out-of-process. Restore that
		// baseline: it's both a detectable process/security-model signal and
		// a real security regression to leave disabled.
		Delete("disable-site-isolation-trials").
		Set("disable-features", "TranslateUI").
		Set("enable-features", "NetworkService")

	if opts.ExecutablePath != "" {
		l = l.Bin(opts.ExecutablePath)
	} else if os.Getenv("GHOSTCHROME_BUNDLED_CHROME") == "" {
		// Prefer the system's real Chrome over rod's bundled Chromium. The
		// bundled build can be many versions behind (an outdated Chrome version
		// is itself an anti-bot signal), and a real install carries a more
		// "normal" fingerprint. The GHOSTCHROME_BUNDLED_CHROME env var opts
		// back into rod's download if that is ever needed.
		if bin := FindSystemChromeBinary(); bin != "" {
			l = l.Bin(bin).
				Set("no-first-run").
				Set("no-default-browser-check")
		}
	}
	if opts.Invisible && !useLegacyHeadless {
		// Off-screen window: Chrome renders normally (full GPU/WebGL/fonts)
		// so anti-bot fingerprints match a real browser, but no UI is visible.
		// Skipped on macOS (handled by useLegacyHeadless above).
		l = l.Set("window-position", "-32000,-32000")
	}
	if needsNoSandbox() {
		l = l.NoSandbox(true)
	}
	if opts.RemotePort > 0 {
		l = l.RemoteDebuggingPort(opts.RemotePort)
	}
	for _, arg := range opts.Args {
		if name, values, ok := splitChromiumArg(arg); ok {
			l = l.Set(flags.Flag(name), values...)
		}
	}
	if opts.UserDataDir != "" {
		l = l.UserDataDir(opts.UserDataDir)
	}
	if opts.Proxy != "" {
		// Chrome's --proxy-server does not support user:pass in the URL;
		// strip credentials here and let ApplyProxyAuth handle auth via CDP.
		proxyForChrome := opts.Proxy
		if u, err := url.Parse(opts.Proxy); err == nil && u.User != nil {
			u.User = nil
			proxyForChrome = u.String()
		}
		l = l.Proxy(proxyForChrome)
		if opts.ProxyBypass != "" {
			l = l.Set("proxy-bypass-list", opts.ProxyBypass)
		}
		l = l.Set("ignore-certificate-errors")
	}
	if len(opts.Extensions) > 0 {
		joined := strings.Join(opts.Extensions, ",")
		l = l.Set("load-extension", joined).
			Set("disable-extensions-except", joined)
	}
	// (System Chrome is now preferred unconditionally above; opts.SystemChrome
	// and the imported-profile marker are kept in the struct for explicit
	// callers but no longer gate binary resolution.)
	// When the profile was imported via `ghostchrome import-profile`, cookies
	// have been rewritten as plaintext. Chrome on macOS would normally still
	// query the Keychain for the os_crypt key and wipe plaintext cookies it
	// considers "stale". Forcing the basic password store disables Keychain
	// integration, so Chrome accepts our plaintext cookies as authoritative.
	if opts.UserDataDir != "" && profileHasDecryptedCookies(opts.UserDataDir) {
		l = l.Set("password-store", "basic").
			Set("use-mock-keychain")
	}
	return l
}

func profileHasDecryptedCookies(userDataDir string) bool {
	if userDataDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(userDataDir, ".ghostchrome-cookies-decrypted"))
	return err == nil
}

// needsNoSandbox reports whether Chrome should be launched with --no-sandbox.
// We check common CI environment markers, root UID (common in containers), and
// Ubuntu 23.10+ AppArmor's restriction on unprivileged user namespaces — which
// otherwise crashes Chrome with "No usable sandbox!". This mirrors Playwright's
// default of disabling the Chromium sandbox: the trust boundary in automation
// is the operator code, not the rendered page.
func needsNoSandbox() bool {
	if os.Geteuid() == 0 {
		return true
	}
	for _, key := range []string{"GITHUB_ACTIONS", "CI", "GHOSTCHROME_NO_SANDBOX", "PLAYWRIGHT_MCP_NO_SANDBOX"} {
		if v := os.Getenv(key); v != "" && v != "0" && v != "false" {
			return true
		}
	}
	if data, err := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns"); err == nil {
		if strings.TrimSpace(string(data)) == "1" {
			return true
		}
	}
	return false
}

// Browser wraps a Rod browser with connect/launch logic.
type Browser struct {
	browser           *rod.Browser
	page              *rod.Page
	timeout           time.Duration
	connected         bool // true if connected to external Chrome (don't close it)
	attachFresh       bool // true when ConnectURL points at a foreign Chrome we must not disturb
	ownership         Ownership
	connectURL        string
	statePath         string
	state             *sessionState
	targetTab         *int
	applyProfile      bool
	managed           bool   // true when ConnectURL points at a ghostchrome-managed session
	contextName       string // non-empty when operating inside a named BrowserContext
	continuousLogPath string

	// providerCleanup releases provider-owned Chrome resources (set only
	// when the browser was provisioned via BrowserOpts.ProviderFunc).
	providerCleanup func()
	ownedLauncher   *launcher.Launcher
	closeOnce       sync.Once

	bgObserver *Observer
}

// BrowserOpts configures NewBrowserWith. It supersedes the positional
// parameters of NewBrowser and adds persistent profile + upstream proxy
// support for auto-launch.
type BrowserOpts struct {
	ConnectURL     string
	Headless       bool
	Invisible      bool
	TimeoutSec     int
	CDPHeaders     map[string]string
	CDPTimeoutMS   int
	UserDataDir    string   // absolute path; ignored when ConnectURL is set
	Proxy          string   // upstream proxy URL; ignored when ConnectURL is set
	ProxyBypass    string   // Chromium proxy bypass list; ignored when ConnectURL is set
	ExecutablePath string   // Chrome/Chromium binary path; ignored when ConnectURL is set
	LaunchArgs     []string // extra Chromium switches; ignored when ConnectURL is set
	Extensions     []string // absolute paths to unpacked extensions; ignored when ConnectURL is set
	SystemChrome   bool     // force /Applications/Google Chrome.app binary; ignored when ConnectURL is set

	// AttachFresh is set when ConnectURL points at a Chrome we don't own
	// (the user's personal browser, discovered via DiscoverCDP). In that
	// case we must NOT reuse any existing tab — every command works in a
	// freshly created background target so the user's foreground tab is
	// left untouched. Ignored in auto-launch mode.
	AttachFresh bool

	// ContextName, when non-empty, routes all page operations through a
	// named isolated BrowserContext (incognito). The name→contextID mapping
	// is persisted in ~/.ghostchrome/contexts.json across CLI invocations.
	// Only meaningful in connected mode (ConnectURL != ""). In auto-launch
	// mode each process already gets its own profile; the flag is ignored.
	ContextName string

	// TargetTabIndex selects an explicit tab index in connected mode.
	// Nil means "use persisted state or auto-select when unambiguous".
	TargetTabIndex *int

	// ApplyProfile, when true, installs ghostchrome's default UA / viewport
	// profile on whichever page Page() resolves to. In auto-launch mode it
	// is on by default (the page is ours). In connected mode it must be
	// explicit — mutating a foreign Chrome tab violates the runtime policy
	// stated in CLAUDE.md.
	ApplyProfile bool

	// ManagedSession is set when ConnectURL points at a Chrome ghostchrome
	// itself spawned and owns (the transparent daemon or an explicit
	// `-s <name>` session). Only those sessions replay the persisted
	// emulation profile across invocations: doing it on a foreign Chrome
	// would mutate a user tab, which the runtime policy forbids.
	ManagedSession bool

	// ProviderFunc, when set, replaces the local launcher for Chrome
	// provisioning. Returns a WS URL and a cleanup function. Ignored
	// when ConnectURL is set (direct connection takes precedence).
	ProviderFunc func(ctx context.Context) (wsURL string, cleanup func(), err error)
}

// NewBrowser creates a browser instance.
// If connectURL is set, connects to an existing Chrome via CDP.
// Otherwise, auto-launches a new Chrome process.
func NewBrowser(connectURL string, headless bool, timeoutSec int) (*Browser, error) {
	return NewBrowserWith(BrowserOpts{
		ConnectURL: connectURL,
		Headless:   headless,
		TimeoutSec: timeoutSec,
	})
}

// NewBrowserWith creates a browser instance with full options. Persistent
// user_data_dir and upstream proxy only apply in auto-launch mode (i.e.,
// when opts.ConnectURL is empty).
func NewBrowserWith(opts BrowserOpts) (*Browser, error) {
	connectURL := opts.ConnectURL
	timeout := time.Duration(opts.TimeoutSec) * time.Second

	var b *rod.Browser
	var state *sessionState
	var providerCleanup func()
	var ownedLauncher *launcher.Launcher
	var statePath string
	if connectURL != "" {
		var err error
		statePath, err = sessionStatePath(connectURL)
		if err != nil {
			return nil, err
		}
		state, err = loadSessionState(statePath)
		if err != nil {
			return nil, err
		}
		b, err = connectRodBrowser(connectURL, timeout, opts.CDPTimeoutMS, opts.CDPHeaders)
		if err != nil {
			return nil, err
		}
		if opts.ContextName != "" {
			b, err = AcquireContext(b, opts.ContextName)
			if err != nil {
				return nil, fmt.Errorf("context %q: %w", opts.ContextName, err)
			}
		}
	} else if opts.ProviderFunc != nil {
		ctx := context.Background()
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		u, cleanup, err := opts.ProviderFunc(ctx)
		if err != nil {
			return nil, fmt.Errorf("provider: %w", err)
		}
		providerCleanup = cleanup
		b, err = connectRodBrowser(u, timeout, opts.CDPTimeoutMS, opts.CDPHeaders)
		if err != nil {
			if cleanup != nil {
				cleanup()
			}
			return nil, err
		}
	} else {
		l := NewLauncher(LauncherOpts{
			Headless:       opts.Headless,
			Invisible:      opts.Invisible,
			UserDataDir:    opts.UserDataDir,
			Proxy:          opts.Proxy,
			ProxyBypass:    opts.ProxyBypass,
			ExecutablePath: opts.ExecutablePath,
			Args:           opts.LaunchArgs,
			Extensions:     opts.Extensions,
			SystemChrome:   opts.SystemChrome,
		})
		ownsUserDataDir := LauncherOwnsRodTempProfile(l, opts.UserDataDir, opts.LaunchArgs)
		u, err := l.Launch()
		if err != nil {
			CleanupFailedLauncher(l, ownsUserDataDir)
			return nil, err
		}
		b, err = connectLaunchedRodBrowser(u, timeout, opts.CDPTimeoutMS, opts.CDPHeaders)
		if err != nil {
			if ownsUserDataDir {
				CleanupLauncher(l, true)
			} else {
				l.Kill()
			}
			return nil, err
		}
		if ownsUserDataDir {
			ownedLauncher = l
		}
	}

	// Auto-launch always applies our profile (the page is ours). Connected
	// mode only applies it when the caller explicitly asked — never on a
	// foreign user tab by default.
	applyProfile := opts.ApplyProfile || connectURL == ""

	continuousLogPath := ""
	if connectURL != "" && opts.ManagedSession {
		continuousLogPath, _ = continuousObserverLogPath(connectURL)
	}

	out := &Browser{
		browser:           b,
		timeout:           timeout,
		connected:         connectURL != "",
		attachFresh:       connectURL != "" && opts.AttachFresh,
		ownership:         browserOwnership(opts, ownedLauncher, providerCleanup),
		connectURL:        connectURL,
		statePath:         statePath,
		state:             state,
		targetTab:         opts.TargetTabIndex,
		applyProfile:      applyProfile,
		managed:           connectURL != "" && opts.ManagedSession,
		contextName:       opts.ContextName,
		continuousLogPath: continuousLogPath,

		providerCleanup: providerCleanup,
		ownedLauncher:   ownedLauncher,
	}
	_ = watcherFor(out.browser)
	return out, nil
}

func browserOwnership(opts BrowserOpts, ownedLauncher *launcher.Launcher, providerCleanup func()) Ownership {
	switch {
	case providerCleanup != nil:
		return OwnershipProvider
	case opts.ManagedSession && opts.ConnectURL != "":
		return OwnershipManaged
	case opts.ConnectURL != "":
		return OwnershipAttached
	case ownedLauncher != nil || opts.ConnectURL == "":
		return OwnershipEphemeral
	default:
		return OwnershipAttached
	}
}

// LauncherOwnsRodTempProfile reports whether the launcher's final user data
// directory is the ephemeral direct child generated by Rod. Explicit profile
// inputs are never owned, even when they happen to live under Rod's prefix.
func LauncherOwnsRodTempProfile(l *launcher.Launcher, userDataDir string, launchArgs []string) bool {
	if userDataDir != "" || defaults.Dir != "" {
		return false
	}
	for _, arg := range launchArgs {
		name, _, ok := splitChromiumArg(arg)
		if ok && flags.Flag(name) == flags.UserDataDir {
			return false
		}
	}

	rawUserDataDir := l.Get(flags.UserDataDir)
	if rawUserDataDir == "" {
		return false
	}
	userDataDir, err := filepath.Abs(rawUserDataDir)
	if err != nil {
		return false
	}
	prefix, err := filepath.Abs(launcher.DefaultUserDataDirPrefix)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(prefix, userDataDir)
	return err == nil && rel != "." && filepath.Dir(rel) == "."
}

var connectLaunchedRodBrowser = connectRodBrowser

// CleanupLauncher synchronously terminates a successfully launched Chrome and
// optionally removes its Rod-owned temporary profile. Kill must precede
// Cleanup: Rod's Cleanup waits on the launcher exit channel and has no context
// or cancellation mechanism of its own.
func CleanupLauncher(l *launcher.Launcher, removeProfile bool) {
	if l == nil {
		return
	}
	l.Kill()
	if removeProfile {
		l.Cleanup()
	}
}

// CleanupFailedLauncher releases resources after Launcher.Launch returned an
// error. A non-zero PID means Chrome did start, so wait for Rod's exit channel
// before deleting. With PID zero no process can still write the generated dir.
func CleanupFailedLauncher(l *launcher.Launcher, removeProfile bool) {
	if l == nil {
		return
	}
	started := l.PID() != 0
	l.Kill()
	if !removeProfile {
		return
	}
	if started {
		l.Cleanup()
		return
	}
	_ = os.RemoveAll(l.Get(flags.UserDataDir))
}

func connectRodBrowser(connectURL string, timeout time.Duration, cdpTimeoutMS int, headers map[string]string) (*rod.Browser, error) {
	if len(headers) == 0 && cdpTimeoutMS <= 0 {
		// Do not wrap Connect in Browser.Timeout: initEvents binds to that
		// context, and CancelTimeout kills the CDP event fan-out. Popup
		// adoption and EventHub then never see Target.targetCreated.
		ctx := context.Background()
		var cancel context.CancelFunc
		if timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		client, err := cdp.StartWithURL(ctx, connectURL, nil)
		if err != nil {
			return nil, err
		}
		b := rod.New().Client(client)
		if err := b.Connect(); err != nil {
			return nil, err
		}
		return b, nil
	}
	ctx := context.Background()
	connectTimeout := timeout
	if cdpTimeoutMS > 0 {
		connectTimeout = time.Duration(cdpTimeoutMS) * time.Millisecond
	}
	if connectTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, connectTimeout)
		defer cancel()
	}
	client, err := cdp.StartWithURL(ctx, connectURL, cdpHeader(headers))
	if err != nil {
		return nil, err
	}
	b := rod.New().Client(client)
	if err := b.Connect(); err != nil {
		return nil, err
	}
	return b, nil
}

func cdpHeader(headers map[string]string) http.Header {
	if len(headers) == 0 {
		return nil
	}
	out := http.Header{}
	for key, value := range headers {
		out.Set(key, value)
	}
	return out
}

func splitChromiumArg(arg string) (name string, values []string, ok bool) {
	arg = strings.TrimSpace(arg)
	if !strings.HasPrefix(arg, "--") || arg == "--" {
		return "", nil, false
	}
	arg = strings.TrimPrefix(arg, "--")
	if arg == "" || strings.HasPrefix(arg, "-") {
		return "", nil, false
	}
	if idx := strings.IndexByte(arg, '='); idx >= 0 {
		name = strings.TrimSpace(arg[:idx])
		value := arg[idx+1:]
		if name == "" {
			return "", nil, false
		}
		return name, []string{value}, true
	}
	return arg, nil, true
}

// maybeApplyProfile installs the default page profile (auto-launch only, see
// ApplyProfile) and then replays the session's persisted emulation profile, in
// that order: the emulation the user explicitly asked for must win over the
// baseline viewport.
func (b *Browser) maybeApplyProfile(p *rod.Page) {
	if b == nil {
		return
	}
	if b.applyProfile {
		_ = ApplyDefaultPageProfile(p)
	}
	b.restoreEmulation(p)
}

// restoreEmulation replays the emulation profile persisted by an earlier
// invocation. CDP emulation overrides die with the DevTools session, so
// without this a managed daemon session loses its viewport between two
// commands.
func (b *Browser) restoreEmulation(p *rod.Page) {
	if b == nil || p == nil || !b.managed || b.state == nil {
		return
	}
	state := b.state.Emulation
	if state.Empty() {
		return
	}
	if err := ApplyEmulationState(p, state); err != nil {
		fmt.Fprintf(os.Stderr, "warning: emulation not restored: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "[emulation] restored %s (clear with `ghostchrome emulate --reset`)\n", state.Summary())
}

// EmulationState returns the emulation profile persisted for this session.
func (b *Browser) EmulationState() EmulationState {
	if b == nil || !b.managed || b.state == nil {
		return EmulationState{}
	}
	return b.state.Emulation
}

// SetEmulationState persists the emulation profile so the next invocation can
// replay it. It is a no-op outside managed sessions (one-shot launches keep
// their overrides for the whole process; foreign Chromes must not be mutated).
func (b *Browser) SetEmulationState(state EmulationState) error {
	if b == nil || !b.managed || b.state == nil {
		return nil
	}
	b.state.Emulation = state
	return saveSessionState(b.statePath, b.state)
}

// ClearEmulationState forgets the persisted emulation profile.
func (b *Browser) ClearEmulationState() error {
	return b.SetEmulationState(EmulationState{})
}

// Page returns the active page or creates a new one.
// When connected to an existing Chrome, it prefers the persisted active tab.
func (b *Browser) Page() (page *rod.Page, err error) {
	defer func() {
		if err == nil && page != nil {
			if scriptErr := ApplyInitScripts(page); scriptErr != nil {
				page, err = nil, fmt.Errorf("init scripts: %w", scriptErr)
			}
		}
	}()
	if b.page != nil {
		return b.page, nil
	}

	if b.connected && !b.attachFresh {
		if b.targetTab != nil {
			p, err := pageAtIndex(b.browser, *b.targetTab)
			if err != nil {
				return nil, err
			}
			b.page = p
			_ = b.setCurrentTargetID(p.TargetID)
			b.maybeApplyProfile(p)
			return b.page, nil
		}

		if b.state != nil && b.state.CurrentTargetID != "" {
			p, err := b.browser.PageFromTarget(proto.TargetTargetID(b.state.CurrentTargetID))
			if err == nil {
				b.page = p
				b.maybeApplyProfile(p)
				return b.page, nil
			}
		}

		pages, err := b.browser.Pages()
		if err != nil {
			return nil, err
		}
		nonBlank := nonBlankPages(pages)
		switch len(nonBlank) {
		case 1:
			b.page = nonBlank[0]
			b.maybeApplyProfile(b.page)
			return b.page, nil
		case 0:
			if len(pages) > 0 {
				b.page = pages[0]
				b.maybeApplyProfile(b.page)
				return b.page, nil
			}
		default:
			// Legacy escape hatch: GHOSTCHROME_TAB_AUTO=first preserves the
			// pre-multi-tab behavior of grabbing pages[0] silently. New
			// callers should use --tab <index> instead.
			if os.Getenv("GHOSTCHROME_TAB_AUTO") == "first" {
				b.page = nonBlank[0]
				b.maybeApplyProfile(b.page)
				return b.page, nil
			}
			return nil, fmt.Errorf("multiple connected tabs available; run `ghostchrome tabs --connect ...` and pass --tab <index> (or set GHOSTCHROME_TAB_AUTO=first)")
		}
	}

	// When attached to an external Chrome we create the new tab in the
	// background so we don't steal focus from whatever the user is doing.
	// In auto-launch mode the field is irrelevant (no visible UI), so we
	// pass the same struct unconditionally.
	p, err := b.browser.Page(proto.TargetCreateTarget{Background: b.connected})
	if err != nil {
		return nil, err
	}
	b.page = p
	_ = b.setCurrentTargetID(p.TargetID)
	b.maybeApplyProfile(p)
	return p, nil
}

// ReacquirePage drops the cached page handle and resolves a new live page
// from the same browser connection. It is intended for long-lived callers
// (MCP/JSONL) after a target was closed or replaced while Chrome itself is
// still alive. The previous target's persisted snapshot is discarded so refs
// from the dead target cannot be used against the replacement page.
//
// ReacquirePage never launches or reconnects Chrome. Callers that cannot
// recover a page from the current browser must close this Browser and use
// NewBrowserWith again, which keeps ownership and reconnect semantics
// explicit.
func (b *Browser) ReacquirePage(previous *rod.Page) (*rod.Page, error) {
	if b == nil || b.browser == nil {
		return nil, fmt.Errorf("browser is not connected")
	}
	if previous != nil {
		if b.page == previous || (b.page != nil && b.page.TargetID == previous.TargetID) {
			b.page = nil
		}
		// A target replacement invalidates all backend-node refs, even when the
		// replacement happens to be assigned the same logical tab index.
		if err := b.deleteSnapshot(previous.TargetID); err != nil {
			return nil, fmt.Errorf("discard stale page snapshot: %w", err)
		}
	}
	return b.Page()
}

func pageAtIndex(browser *rod.Browser, index int) (*rod.Page, error) {
	pages, err := browser.Pages()
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(pages) {
		return nil, fmt.Errorf("tab index %d out of range (0-%d)", index, len(pages)-1)
	}
	return pages[index], nil
}

func nonBlankPages(pages rod.Pages) rod.Pages {
	if len(pages) == 0 {
		return nil
	}
	// Each page.Info() is a sync CDP roundtrip. With many tabs this becomes
	// the dominant cost of every command; fan out and keep the returned
	// order stable.
	keep := make([]bool, len(pages))
	var wg sync.WaitGroup
	wg.Add(len(pages))
	for i, page := range pages {
		i, page := i, page
		go func() {
			defer wg.Done()
			info, err := page.Info()
			if err != nil || info == nil {
				return
			}
			if info.URL == "" || info.URL == "about:blank" {
				return
			}
			keep[i] = true
		}()
	}
	wg.Wait()

	out := make(rod.Pages, 0, len(pages))
	for i, page := range pages {
		if keep[i] {
			out = append(out, page)
		}
	}
	return out
}

// Connected returns true if connected to external Chrome (not launched by us).
func (b *Browser) Connected() bool {
	return b.connected
}

// ConnectURL returns the resolved CDP endpoint used by this browser. Managed
// sessions and --connect=auto resolve their endpoint before Browser creation,
// so detached helpers must use this value instead of the original CLI flag.
func (b *Browser) ConnectURL() string {
	if b == nil {
		return ""
	}
	return b.connectURL
}

// RodBrowser returns the underlying rod.Browser for advanced operations.
func (b *Browser) RodBrowser() *rod.Browser {
	return b.browser
}

// Alive reports whether the underlying Chrome still answers CDP within d.
// A crashed or killed Chrome leaves the rod handle poisoned — every call
// on it fails with a context error — so callers holding a long-lived
// Browser use this cheap probe (one Browser.getVersion round-trip, ~1ms
// on a live process, instant failure on a dead one) before trusting it.
func (b *Browser) Alive(d time.Duration) bool {
	if b == nil || b.browser == nil {
		return false
	}
	_, err := b.browser.Timeout(d).Version()
	return err == nil
}

// SetCurrentPage marks the provided page as the current tab for the session.
func (b *Browser) SetCurrentPage(page *rod.Page) error {
	if page == nil {
		return nil
	}
	if err := ApplyInitScripts(page); err != nil {
		return fmt.Errorf("init scripts: %w", err)
	}
	b.page = page
	return b.setCurrentTargetID(page.TargetID)
}

// stripSSRForCache returns a copy of result with SSRPayloads cleared, safe to
// persist to the on-disk cache. SSRPayloads are only populated on the
// SSR-fallback opt-in path (see ExtractionResult.SSRPayloads) and can carry
// tokens/PII from an authenticated page, so they must never be cached where a
// later non-SSR command (CachedExtract) could read them back. The original
// result is never mutated — the caller's own copy (used for the immediate
// command response) is returned untouched when there is nothing to strip.
func stripSSRForCache(result *ExtractionResult) *ExtractionResult {
	if result == nil || len(result.SSRPayloads) == 0 {
		return result
	}
	stripped := *result
	stripped.SSRPayloads = nil
	return &stripped
}

// SaveSnapshot persists the latest ref snapshot for the page.
//
// SSRPayloads are stripped from the persisted copy before caching (see
// stripSSRForCache) — the caller's result, used for the immediate command
// response, is left untouched; only the copy written to disk is stripped, so
// a later non-SSR command reading this cache via CachedExtract can never leak
// SSR data it didn't ask for.
func (b *Browser) SaveSnapshot(page *rod.Page, result *ExtractionResult) error {
	if !b.connected || b.state == nil || page == nil || result == nil {
		return nil
	}
	snapshot, err := snapshotFromResult(page, stripSSRForCache(result))
	if err != nil {
		return err
	}
	b.state.Snapshots[snapshot.TargetID] = *snapshot
	b.state.CurrentTargetID = snapshot.TargetID
	b.page = page
	return saveSessionState(b.statePath, b.state)
}

// CachedExtract returns the cached ExtractionResult for the current page if
// the URL has not changed since the last extraction. Returns nil when no cache
// exists or the page has navigated. This avoids the expensive CDP
// AccessibilityGetFullAXTree call when the page content is unchanged.
//
// The URL check uses a lightweight JS eval instead of page.Info() to minimize
// CDP round-trips.
func (b *Browser) CachedExtract(page *rod.Page) *ExtractionResult {
	if !b.connected || b.state == nil || page == nil {
		return nil
	}
	snap := b.snapshotByTarget(page.TargetID)
	if snap == nil || snap.CachedExtraction == nil || snap.URL == "" {
		return nil
	}
	currentURL, err := page.Eval("() => location.href")
	if err != nil {
		return nil
	}
	if currentURL.Value.Str() != snap.URL {
		return nil
	}
	return snap.CachedExtraction
}

// InvalidateCachedExtract drops the cached accessibility dump for page so the
// next snapshot re-extracts after a mutating action. Refs stay in place so
// click/type can still resolve @N until a fresh extract overwrites them.
func (b *Browser) InvalidateCachedExtract(page *rod.Page) error {
	if !b.connected || b.state == nil || page == nil {
		return nil
	}
	snap := b.snapshotByTarget(page.TargetID)
	if snap == nil || snap.CachedExtraction == nil {
		return nil
	}
	snap.CachedExtraction = nil
	b.state.Snapshots[string(page.TargetID)] = *snap
	return saveSessionState(b.statePath, b.state)
}

// Snapshot returns the last persisted snapshot for the current page.
func (b *Browser) Snapshot(page *rod.Page) *PageSnapshot {
	if !b.connected || b.state == nil || page == nil {
		return nil
	}
	return b.snapshotByTarget(page.TargetID)
}

// CurrentTargetID returns the persisted current tab target, if any.
func (b *Browser) CurrentTargetID() string {
	if b.connected && b.state != nil {
		return b.state.CurrentTargetID
	}
	if b.page != nil {
		return string(b.page.TargetID)
	}
	return ""
}

func (b *Browser) snapshotByTarget(targetID proto.TargetTargetID) *PageSnapshot {
	if b.state == nil {
		return nil
	}
	snapshot, ok := b.state.Snapshots[string(targetID)]
	if !ok {
		return nil
	}
	copy := snapshot
	return &copy
}

func (b *Browser) setCurrentTargetID(targetID proto.TargetTargetID) error {
	if !b.connected || b.state == nil {
		return nil
	}
	b.state.CurrentTargetID = string(targetID)
	return saveSessionState(b.statePath, b.state)
}

func (b *Browser) deleteSnapshot(targetID proto.TargetTargetID) error {
	if !b.connected || b.state == nil {
		return nil
	}
	delete(b.state.Snapshots, string(targetID))
	if b.state.CurrentTargetID == string(targetID) {
		b.state.CurrentTargetID = ""
	}
	return saveSessionState(b.statePath, b.state)
}

// DeleteSnapshot removes stored ref state for a closed page target.
func (b *Browser) DeleteSnapshot(targetID proto.TargetTargetID) error {
	return b.deleteSnapshot(targetID)
}

const maxPlaywrightLogEntries = 1000

// AppendConsoleLog appends console events to the persistent session log.
func (b *Browser) AppendConsoleLog(events []ObserverEvent) error {
	if !b.connected || b.state == nil || len(events) == 0 {
		return nil
	}
	if b.hasContinuousObserverLog() {
		return nil
	}
	b.state.PlaywrightLog.Console = appendBoundedConsole(b.state.PlaywrightLog.Console, events, maxPlaywrightLogEntries)
	return saveSessionState(b.statePath, b.state)
}

// ConsoleLog returns a copy of the persistent console log.
func (b *Browser) ConsoleLog() []ObserverEvent {
	if events, ok := b.continuousEvents(); ok {
		out := make([]ObserverEvent, 0, len(events))
		for _, event := range events {
			if event.Kind == KindConsole || event.Kind == KindError {
				out = append(out, event)
			}
		}
		return out
	}
	if !b.connected || b.state == nil || len(b.state.PlaywrightLog.Console) == 0 {
		return nil
	}
	out := make([]ObserverEvent, len(b.state.PlaywrightLog.Console))
	copy(out, b.state.PlaywrightLog.Console)
	return out
}

// ClearConsoleLog removes the persistent console log for this session.
func (b *Browser) ClearConsoleLog() error {
	if !b.connected || b.state == nil {
		return nil
	}
	b.state.PlaywrightLog.Console = nil
	if b.continuousLogPath != "" {
		if err := appendContinuousClear(b.continuousLogPath, KindConsole); err != nil {
			return err
		}
	}
	return saveSessionState(b.statePath, b.state)
}

// AppendNetworkLog appends network entries to the persistent session log.
func (b *Browser) AppendNetworkLog(entries []*CapturedEntry) error {
	if !b.connected || b.state == nil || len(entries) == 0 {
		return nil
	}
	if b.hasContinuousObserverLog() {
		return nil
	}
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		b.state.PlaywrightLog.Network = append(b.state.PlaywrightLog.Network, *entry)
	}
	b.state.PlaywrightLog.Network = trimNetworkLog(b.state.PlaywrightLog.Network, maxPlaywrightLogEntries)
	return saveSessionState(b.statePath, b.state)
}

// NetworkLog returns a copy of the persistent network log.
func (b *Browser) NetworkLog() []*CapturedEntry {
	if events, ok := b.continuousEvents(); ok {
		out := make([]*CapturedEntry, 0, len(events))
		for _, event := range events {
			if event.Kind != KindNet {
				continue
			}
			out = append(out, &CapturedEntry{
				RequestID:    event.RequestID,
				Method:       event.Method,
				URL:          event.URL,
				ResourceType: event.Type,
				Status:       event.Status,
				MimeType:     event.MimeType,
				ReqHeaders:   event.ReqHeaders,
				ResHeaders:   event.ResHeaders,
				PostData:     event.PostData,
				Body:         event.Body,
				BodyBase64:   event.BodyBase64,
				BodyError:    event.BodyError,
				StartedAt:    time.UnixMilli(event.TS).UTC().Format(time.RFC3339Nano),
			})
		}
		return out
	}
	if !b.connected || b.state == nil || len(b.state.PlaywrightLog.Network) == 0 {
		return nil
	}
	out := make([]*CapturedEntry, 0, len(b.state.PlaywrightLog.Network))
	for i := range b.state.PlaywrightLog.Network {
		entry := b.state.PlaywrightLog.Network[i]
		out = append(out, &entry)
	}
	return out
}

// ClearNetworkLog removes the persistent network log for this session.
func (b *Browser) ClearNetworkLog() error {
	if !b.connected || b.state == nil {
		return nil
	}
	b.state.PlaywrightLog.Network = nil
	if b.continuousLogPath != "" {
		if err := appendContinuousClear(b.continuousLogPath, KindNet); err != nil {
			return err
		}
	}
	return saveSessionState(b.statePath, b.state)
}

func (b *Browser) hasContinuousObserverLog() bool {
	if b == nil || b.continuousLogPath == "" {
		return false
	}
	_, err := os.Stat(b.continuousLogPath)
	return err == nil
}

func (b *Browser) continuousEvents() ([]ObserverEvent, bool) {
	if !b.hasContinuousObserverLog() {
		return nil, false
	}
	events, err := readContinuousEvents(b.continuousLogPath)
	if err != nil {
		return nil, false
	}
	return events, true
}

// BrowserTraceState returns the persisted CDP tracing state for this session.
func (b *Browser) BrowserTraceState() BrowserTraceState {
	if !b.connected || b.state == nil {
		return BrowserTraceState{}
	}
	return b.state.BrowserTrace
}

// SetBrowserTraceState stores the CDP tracing state for this session.
func (b *Browser) SetBrowserTraceState(state BrowserTraceState) error {
	if !b.connected || b.state == nil {
		return nil
	}
	b.state.BrowserTrace = state
	return saveSessionState(b.statePath, b.state)
}

// VideoState returns the persisted video metadata for this session.
func (b *Browser) VideoState() VideoState {
	if !b.connected || b.state == nil {
		return VideoState{}
	}
	return b.state.Video
}

// SetVideoState stores video metadata for this session.
func (b *Browser) SetVideoState(state VideoState) error {
	if !b.connected || b.state == nil {
		return nil
	}
	previous := b.state.Video
	b.state.Video = state
	if err := saveSessionState(b.statePath, b.state); err != nil {
		b.state.Video = previous
		return err
	}
	return nil
}

func appendBoundedConsole(existing, incoming []ObserverEvent, max int) []ObserverEvent {
	out := make([]ObserverEvent, 0, len(existing)+len(incoming))
	out = append(out, existing...)
	out = append(out, incoming...)
	if len(out) <= max {
		return out
	}
	return append([]ObserverEvent(nil), out[len(out)-max:]...)
}

func trimNetworkLog(entries []CapturedEntry, max int) []CapturedEntry {
	if len(entries) <= max {
		return entries
	}
	return append([]CapturedEntry(nil), entries[len(entries)-max:]...)
}

// Close cleans up the browser resources.
// External Chrome keeps running; the CLI process owns the websocket lifetime.
func (b *Browser) StartBackgroundObserver(page *rod.Page) {
	if b.bgObserver != nil || !b.connected || b.hasContinuousObserverLog() {
		return
	}
	obs := NewObserver(page, ObserverOpts{BufferSize: 512})
	if err := obs.Start(context.Background()); err != nil {
		return
	}
	b.bgObserver = obs
}

func (b *Browser) drainBackgroundObserver() {
	if b.bgObserver == nil {
		return
	}
	observer := b.bgObserver
	b.bgObserver = nil
	_ = observer.Stop()
	events := observer.Drain(0)
	if len(events) == 0 {
		return
	}
	var consoleEvents []ObserverEvent
	var netEntries []*CapturedEntry
	for _, e := range events {
		switch e.Kind {
		case KindConsole, KindError:
			consoleEvents = append(consoleEvents, e)
		case KindNet:
			netEntries = append(netEntries, &CapturedEntry{
				RequestID:    e.RequestID,
				Method:       e.Method,
				URL:          e.URL,
				Status:       e.Status,
				MimeType:     e.MimeType,
				ResourceType: e.Type,
				ReqHeaders:   e.ReqHeaders,
				ResHeaders:   e.ResHeaders,
				PostData:     e.PostData,
				Body:         e.Body,
				BodyBase64:   e.BodyBase64,
				BodyError:    e.BodyError,
			})
		}
	}
	if len(consoleEvents) > 0 {
		_ = b.AppendConsoleLog(consoleEvents)
	}
	if len(netEntries) > 0 {
		_ = b.AppendNetworkLog(netEntries)
	}
}

func (b *Browser) Close() {
	b.closeOnce.Do(func() {
		b.drainBackgroundObserver()
		if b.browser != nil {
			stopBrowserEventHubs(b.browser)
			forgetBrowserEventState(b.browser)
			StopPopupWatch(b.browser)
			switch b.ownership {
			case OwnershipAttached, OwnershipManaged:
				if b.attachFresh && b.page != nil {
					_ = b.page.Close()
				}
			case OwnershipProvider:
				// Provider callback owns process teardown.
			default:
				_ = b.browser.Close()
			}
		}
		if b.ownedLauncher != nil {
			CleanupLauncher(b.ownedLauncher, true)
		}
		if b.providerCleanup != nil {
			b.providerCleanup()
		}
	})
}
