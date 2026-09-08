package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/policy"
	"github.com/dev-toolings/ghostchrome/internal/setup"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var (
	flagConnect              string
	flagSession              string
	flagContext              string
	flagTab                  int
	flagHeadless             bool
	flagHeaded               bool
	flagInvisible            bool
	flagTimeout              int
	flagFormat               string
	flagJSON                 bool
	flagRaw                  bool
	flagOutputMaxSize        int
	flagSecretsFile          string
	flagOutputSecrets        []string
	flagProfile              string
	flagUserProfile          string
	flagUserDataDir          string
	flagProxy                string
	flagProxyBypass          string
	flagStealth              bool
	flagEvadeRuntime         bool
	flagTimezone             string
	flagDismissCookies       bool
	flagWaitSelector         string
	flagWaitMs               int
	flagHuman                bool
	flagExtensions           string
	flagDefaultExtensions    bool
	flagObserve              bool
	flagObserveOut           string
	flagPolicy               string
	flagAllowedDomains       string
	flagConfig               string
	flagAutoVideoSize        string
	flagAutoVideoSource      string
	flagConfigViewportW      int
	flagConfigViewportH      int
	flagConfigUserAgent      string
	flagConfigStorageState   string
	flagConfigLocale         string
	flagConfigPermissions    []string
	flagConfigServiceWorkers string
	flagConfigInitScripts    []string
	flagConfigExecutablePath string
	flagConfigLaunchArgs     []string
	flagConfigOutputDir      string
	flagConfigDevice         string
	flagConfigIgnoreHTTPSErr bool
	flagConfigCDPHeaders     map[string]string
	flagConfigCDPTimeoutMS   int
)

var rootCmd = &cobra.Command{
	Use:   "ghostchrome",
	Short: "Ultra-light browser automation CLI for LLM agents",
	Long: `ghostchrome — a single Go binary that controls Chrome via CDP.
Designed for LLM agents: compact output, minimal tokens, zero overhead.

═══════════════════════════════════════════════════════════════════════
QUICK REFERENCE
═══════════════════════════════════════════════════════════════════════

  SESSION         How Chrome runs and authenticates
    serve            Start a persistent Chrome, print WS URL for --connect
    login            Open Chrome non-headless to login a site (LinkedIn, ...)
    import-profile   Clone an existing Chrome profile (cookies via Keychain)
    extensions       Manage bundled Chrome extensions

  NAVIGATE        Move around the page
    navigate         Go to a URL (with wait strategy)
    back / forward   Browser history
    scroll, scroll-by, scroll-to    Scroll viewport / element
    viewport         Resize the viewport
    emulate          Override device / UA / color-scheme / timezone
    geolocation      Override or clear browser geolocation
    tabs             List open tabs

  OBSERVE         Read the page (no side effects)
    extract          Compact a11y tree (skeleton/content/full) + @refs
    preview          Health snapshot: status + errors + network + DOM
    eval             Run JS, return the result
    errors           Console + network errors
    capture          Passively record network requests (JSON HAR-like)
    screenshot       Capture page or element (with optional pixel-diff)
    pdf              Print page as PDF
    perf             Web Vitals (TTFB / FCP / LCP / CLS)
    collect          Auto-detect & extract listings from one+ pages

  INTERACT        Act on elements (use refs from extract)
    click            Click an @ref or by-role/by-name/by-label/by-text
    type             Type text into an input
    press            Press a keyboard key
    hover            Hover an element
    select           Pick option(s) in <select>
    fill-form        Bulk fill {@ref: value} from JSON
    upload           Attach files to <input type=file>
    dialog           Handle alert/confirm/prompt dialogs

  WAIT            Block until a condition holds
    waitfor          Selector / URL change / title / count
    wait-port        TCP port becomes reachable
    wait-url         URL returns expected HTTP status

  STATE           Persist or replay browser state
    cookies          CRUD cookies via CDP
    storage          Save / load cookies + localStorage to JSON
    intercept        Block or fulfill requests by URL pattern
    assert           Assertion verbs with exit codes
    batch            Run multiple commands in-process, single JSON result

  RECIPES         Site-specific scrapers (isolated under packages/)
    linkedin people  → LinkedIn People search → CSV (leads gen)
    linkedin posts   → LinkedIn Content search with filters (freelance hunt)

═══════════════════════════════════════════════════════════════════════
COMMON WORKFLOWS
═══════════════════════════════════════════════════════════════════════

  Zero-config (daemon auto-spawns a background Chrome):
    ghostchrome preview https://example.com
    ghostchrome extract https://example.com
    ghostchrome click @3

  Persistent profile (login once, scrape forever):
    ghostchrome --user-profile mysite login mysite           # interactive
    ghostchrome --user-profile mysite extract https://...    # headless reuse

  Named sessions (parallel isolated browsers):
    ghostchrome -s work goto https://app.example.com
    ghostchrome -s research goto https://scholar.google.com

  Cron-ready batch:
    ghostchrome batch --script run.json   # multi-step in one Chrome run

═══════════════════════════════════════════════════════════════════════

Run 'ghostchrome <command> --help' for full options on a specific command.
Run 'ghostchrome linkedin --help' to see LinkedIn recipes.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Best-effort .env loading. Silent when absent. Loaded BEFORE flag
		// validation so that env-driven defaults (e.g. ANTHROPIC_API_KEY,
		// GHOSTCHROME_PROXY) are visible to subcommands that read os.Getenv.
		_ = godotenv.Load()
		if flagProxy == "" {
			if envProxy := os.Getenv("GHOSTCHROME_PROXY"); envProxy != "" {
				flagProxy = envProxy
			}
		}
		if err := applyPlaywrightConfig(cmd); err != nil {
			return err
		}
		if flagJSON {
			flagFormat = "json"
		}
		if flagOutputMaxSize < 0 {
			return fmt.Errorf("invalid --output-max-size %d: use zero or a positive byte count", flagOutputMaxSize)
		}
		if err := loadConfiguredOutputSecrets(); err != nil {
			return err
		}
		if flagHeaded {
			flagHeadless = false
		}
		switch flagFormat {
		case "text", "json":
		default:
			return fmt.Errorf("invalid format %q: use text or json", flagFormat)
		}
		switch flagProfile {
		case "auto", "human", "agent":
		default:
			return fmt.Errorf("invalid profile %q: use auto, human, or agent", flagProfile)
		}
		engine.SetHumanMode(flagHuman)

		// Runtime.enable evasion (anti-bot): flag wins, else env opt-in.
		if !flagEvadeRuntime && os.Getenv("GHOSTCHROME_EVADE_RUNTIME") == "1" {
			flagEvadeRuntime = true
		}
		engine.SetEvadeRuntimeEnable(flagEvadeRuntime)
		if flagTimezone != "" {
			if _, err := time.LoadLocation(flagTimezone); err != nil {
				return fmt.Errorf("invalid --timezone %q: %w", flagTimezone, err)
			}
		}
		engine.SetStealthTimezone(flagTimezone)

		if flagPolicy != "" {
			p, err := policy.Load(flagPolicy)
			if err != nil {
				return fmt.Errorf("load policy: %w", err)
			}
			engine.ActivePolicy = p
		} else if flagAllowedDomains != "" {
			domains := strings.Split(flagAllowedDomains, ",")
			for i := range domains {
				domains[i] = strings.TrimSpace(domains[i])
			}
			engine.ActivePolicy = policy.FromDomains(domains)
		}
		return nil
	},
}

// commandGroups maps a command name → cobra group ID. Cobra renders the
// command listing grouped by these IDs in the order they are added below.
// Anything not in the map falls into the default "other" bucket.
var commandGroups = map[string]string{
	// Session
	"serve":          "session",
	"login":          "session",
	"import-profile": "session",
	"extensions":     "session",

	// Navigate
	"navigate":    "navigate",
	"back":        "navigate",
	"forward":     "navigate",
	"scroll":      "navigate",
	"scroll-by":   "navigate",
	"scroll-to":   "navigate",
	"viewport":    "navigate",
	"emulate":     "navigate",
	"geolocation": "navigate",
	"tabs":        "navigate",

	// Observe
	"extract":    "observe",
	"preview":    "observe",
	"eval":       "observe",
	"errors":     "observe",
	"capture":    "observe",
	"screenshot": "observe",
	"pdf":        "observe",
	"perf":       "observe",
	"collect":    "observe",
	"watch":      "observe",

	// Interact
	"click":     "interact",
	"type":      "interact",
	"press":     "interact",
	"hover":     "interact",
	"select":    "interact",
	"fill-form": "interact",
	"upload":    "interact",
	"dialog":    "interact",
	"drag":      "interact",
	"clipboard": "interact",
	"mouse":     "interact",

	// Wait
	"waitfor":   "wait",
	"wait-port": "wait",
	"wait-url":  "wait",

	// State
	"cookies":      "state",
	"storage":      "state",
	"intercept":    "state",
	"assert":       "state",
	"batch":        "state",
	"trace-replay": "state",
	"trace-export": "state",
	"trace-clear":  "state",

	// Utility
	"doctor":      "util",
	"init-script": "util",
	"dashboard":   "util",
	"react":       "observe",

	// Recipes (site-specific scrapers under packages/)
	"linkedin":      "recipes",
	"leboncoin":     "recipes",
	"autoscout24":   "recipes",
	"cars-listings": "recipes",
}

func registerGroups() {
	rootCmd.AddGroup(
		&cobra.Group{ID: "session", Title: "Session — Chrome lifecycle & auth:"},
		&cobra.Group{ID: "navigate", Title: "Navigate — move around the page:"},
		&cobra.Group{ID: "observe", Title: "Observe — read the page (no side effects):"},
		&cobra.Group{ID: "interact", Title: "Interact — act on elements:"},
		&cobra.Group{ID: "wait", Title: "Wait — block until a condition holds:"},
		&cobra.Group{ID: "state", Title: "State — persist / replay / assert:"},
		&cobra.Group{ID: "util", Title: "Utility — diagnostics & configuration:"},
		&cobra.Group{ID: "recipes", Title: "Recipes — site-specific scrapers (packages/):"},
	)
	for _, c := range rootCmd.Commands() {
		if g, ok := commandGroups[c.Name()]; ok {
			c.GroupID = g
		}
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagConnect, "connect", "", "WebSocket URL to connect to existing Chrome (e.g. ws://127.0.0.1:9222), or \"auto\" to discover one on 127.0.0.1:9222-9229 and work in a background tab")
	rootCmd.PersistentFlags().StringVarP(&flagSession, "session", "s", "", "Named auto-managed session: spawns a persistent Chrome (bound to profile <name>) on first use and reuses it across calls. Defaults from $PLAYWRIGHT_CLI_SESSION, then $GHOSTCHROME_SESSION. Manage with `ghostchrome sessions`.")
	rootCmd.PersistentFlags().StringVar(&flagContext, "context", "", "Named isolated BrowserContext (incognito) within the connected Chrome; enables parallel sessions without spawning multiple Chrome processes. Only meaningful with --connect.")
	rootCmd.PersistentFlags().IntVar(&flagTab, "tab", -1, "Target tab index when using --connect (use `ghostchrome tabs --connect ...` to list)")
	rootCmd.PersistentFlags().BoolVar(&flagHeadless, "headless", true, "Run Chrome in headless mode")
	rootCmd.PersistentFlags().BoolVar(&flagHeaded, "headed", false, "Run Chrome with a visible browser window (Playwright CLI-compatible inverse of --headless)")
	rootCmd.PersistentFlags().BoolVar(&flagInvisible, "invisible", false, "Run Chrome headful but positioned off-screen (best for anti-bot: real GPU/fingerprint, no visible window). Implies --headless=false.")
	rootCmd.PersistentFlags().IntVar(&flagTimeout, "timeout", 30, "Timeout in seconds for operations")
	rootCmd.PersistentFlags().StringVar(&flagFormat, "format", "text", "Output format: json or text")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "Wrap command output as JSON (Playwright CLI-compatible alias)")
	rootCmd.PersistentFlags().BoolVar(&flagRaw, "raw", false, "Output only the result value without page status or generated sections")
	rootCmd.PersistentFlags().IntVar(&flagOutputMaxSize, "output-max-size", 0, "Maximum stdout payload in bytes; larger output is written to an artifact (0 = unlimited)")
	rootCmd.PersistentFlags().StringVar(&flagSecretsFile, "secrets-file", "", "Dotenv file whose values are redacted from stdout and output artifacts")
	rootCmd.PersistentFlags().StringVar(&flagProfile, "profile", "auto", "Output profile: auto (detect agent env), human, or agent (compact)")
	rootCmd.PersistentFlags().BoolVar(&flagStealth, "stealth", false, "Enable stealth mode (hide headless fingerprints)")
	rootCmd.PersistentFlags().BoolVar(&flagEvadeRuntime, "evade-runtime", false, "Avoid CDP Runtime.enable (rebrowser-style anti-bot evasion; disables JS console-error capture). Also via GHOSTCHROME_EVADE_RUNTIME=1")
	rootCmd.PersistentFlags().StringVar(&flagTimezone, "timezone", "", "Override the JS Date/Intl timezone applied under --stealth (IANA name, e.g. Europe/Paris). Unset + --stealth derives a default from the detected locale (fr* -> Europe/Paris); other locales keep the host timezone.")
	rootCmd.PersistentFlags().BoolVar(&flagDismissCookies, "dismiss-cookies", false, "Auto-dismiss cookie consent banners")
	rootCmd.PersistentFlags().StringVar(&flagWaitSelector, "wait-selector", "", "After navigation, wait for this CSS selector to be visible (useful for SPAs)")
	rootCmd.PersistentFlags().IntVar(&flagWaitMs, "wait-ms", 0, "After navigation (and after --wait-selector if any), sleep this many ms (lets late XHR finish)")
	rootCmd.PersistentFlags().StringVar(&flagUserProfile, "user-profile", "", "Persistent Chrome profile name (auto-launch only): cookies/cache stored under ~/.ghostchrome/profiles/<name>")
	rootCmd.PersistentFlags().StringVar(&flagProxy, "proxy", "", "Upstream proxy URL for Chrome (auto-launch only), e.g. http://user:pass@host:port or socks5://host:1080")
	rootCmd.PersistentFlags().StringVar(&flagProxyBypass, "proxy-bypass", "", "Comma-separated Chromium proxy bypass list (auto-launch only)")
	rootCmd.PersistentFlags().BoolVar(&flagHuman, "human", false, "Simulate human input dynamics (Bezier mouse paths, jittered key delays, occasional typos)")
	rootCmd.PersistentFlags().StringVar(&flagExtensions, "extensions", "", "Comma-separated absolute paths to unpacked Chrome extensions (auto-launch only)")
	rootCmd.PersistentFlags().BoolVar(&flagDefaultExtensions, "default-extensions", false, "Load bundled defaults (uBlock Lite, ICDC, Force Background Tab) from ~/.ghostchrome/extensions/* (auto-launch only)")
	rootCmd.PersistentFlags().BoolVar(&flagObserve, "observe", false, "Capture CDP events during command execution")
	rootCmd.PersistentFlags().StringVar(&flagObserveOut, "observe-out", "", "Path to write NDJSON observer events (default: inline in agent response)")
	rootCmd.PersistentFlags().StringVar(&flagPolicy, "policy", "", "Path to a JSON policy file restricting navigation, eval, upload, clipboard")
	rootCmd.PersistentFlags().StringVar(&flagAllowedDomains, "allowed-domains", "", "Comma-separated domain allowlist (e.g. \"*.example.com,api.stripe.com\"); shorthand for a policy with only allowed_domains")
	rootCmd.PersistentFlags().StringVar(&flagConfig, "config", "", "Playwright CLI-compatible JSON config path (auto-loads .playwright/cli.config.json when present)")
}

func SetVersion(v string) {
	rootCmd.Version = v
	setup.SetVersion(v)
}

func Execute() error {
	registerGroups()
	return rootCmd.Execute()
}

// Cleanups a command registered before it may exit. `os.Exit` does not run
// deferred functions, so a command that fails through `exitErr` never reached
// its own `defer b.Close()`. When the browser was attached with
// `--connect=auto`, the page that Close would have shut is a live renderer in a
// Chrome that outlives the process: every failed invocation leaves one behind,
// holding ~90 MiB until the browser is recycled. Draining the stack here makes
// the failure path release what the success path already released.
var (
	cleanupMu sync.Mutex
	cleanups  []func()
)

func registerCleanup(fn func()) {
	cleanupMu.Lock()
	cleanups = append(cleanups, fn)
	cleanupMu.Unlock()
}

// runCleanups drains the stack in reverse registration order. It is safe to
// call twice — the second call finds it empty — so a command keeping its own
// `defer` costs nothing.
func runCleanups() {
	cleanupMu.Lock()
	pending := cleanups
	cleanups = nil
	cleanupMu.Unlock()

	for i := len(pending) - 1; i >= 0; i-- {
		pending[i]()
	}
}

// exitNow is `os.Exit` for the paths that can be reached with a page open: it
// releases the page first. Use it instead of a bare `os.Exit` anywhere after
// openPage.
func exitNow(code int) {
	runCleanups()
	os.Exit(code)
}

func exitErr(msg string, err error) {
	fmt.Fprintf(os.Stderr, "error: %s: %v\n", msg, err)
	exitNow(1)
}
