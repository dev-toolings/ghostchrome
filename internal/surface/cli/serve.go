package cli

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/spf13/cobra"
)

var flagPort int

const (
	// How long a single Chrome liveness probe may take before it counts as a
	// failure, and how many consecutive failures mean Chrome is really gone.
	//
	// Both matter on a loaded host. A busy Chrome answers /json/version late,
	// not never: with the previous 1.5s single-strike check, a scraper on a
	// 2-vCPU VM tore down its browser thousands of times without a single real
	// crash behind it. 5s × 3 strikes on a 3s tick means a genuine death is
	// still reported within ~10-15s, while a rendering hiccup costs nothing.
	serveProbeTimeout  = 5 * time.Second
	serveHealthStrikes = 3
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Launch a persistent Chrome and print the WebSocket debugger URL",
	Long: `Serve launches a long-lived Chrome process and prints the WebSocket URL.
Other ghostchrome commands connect to it via --connect, eliminating the
~4s cold start on every call.

Examples:
  ghostchrome serve --stealth
  ghostchrome serve --stealth --port 9222
  ghostchrome serve --headless=false --stealth

Then in another terminal:
  ghostchrome collect https://... --connect ws://127.0.0.1:9222/...
  ghostchrome preview https://... --connect ws://127.0.0.1:9222/...`,
	Run: func(cmd *cobra.Command, args []string) {
		skipImplicitDaemon = true
		opts := buildBrowserOpts()
		launcherOpts := engine.LauncherOpts{
			Headless:       flagHeadless,
			Invisible:      flagInvisible,
			RemotePort:     flagPort,
			UserDataDir:    opts.UserDataDir,
			Proxy:          opts.Proxy,
			ProxyBypass:    opts.ProxyBypass,
			Extensions:     opts.Extensions,
			ExecutablePath: opts.ExecutablePath,
			Args:           opts.LaunchArgs,
		}
		wsURL, cleanup, err := launchServeChrome(launcherOpts)
		if err != nil {
			exitErr("launch chrome", err)
		}
		defer cleanup()
		continuousObserver, observerErr := engine.StartPersistentSessionObserver(wsURL)
		if observerErr != nil {
			fmt.Fprintf(os.Stderr, "warning: continuous observer unavailable: %v\n", observerErr)
		} else {
			defer continuousObserver.Stop()
		}

		// Apply stealth to a warm-up page
		if flagStealth {
			if err := warmUpServeChrome(wsURL, cleanup); err != nil {
				exitErr("stealth", err)
			}
		}

		fmt.Fprintf(os.Stderr, "Chrome ready. Connect with:\n")
		fmt.Fprintf(os.Stderr, "  ghostchrome <cmd> --connect '%s'\n\n", wsURL)
		fmt.Println(wsURL)

		// Resolve the CDP port so we can detect Chrome dying and not linger as
		// an orphan serve. flagPort is set in session mode; otherwise parse it
		// from the WebSocket URL.
		port := flagPort
		if port == 0 {
			if u, perr := url.Parse(wsURL); perr == nil {
				if p, aerr := strconv.Atoi(u.Port()); aerr == nil {
					port = p
				}
			}
		}

		// Idle shutdown. Defaults to 1h so a forgotten daemon cannot squat RAM forever
		// (see the runtime policy in CLAUDE.md); when GHOSTCHROME_IDLE_TIMEOUT is
		// set, serve exits after that long with no browser activity — matching
		// agent-browser's AGENT_BROWSER_IDLE_TIMEOUT_MS and bounding the disk/RAM
		// growth of a forgotten daemon.
		idleTimeout := serveIdleTimeoutForSession(flagHeadless)
		var lastActivity time.Time
		var lastTargets string
		if idleTimeout > 0 && port > 0 {
			lastActivity = time.Now()
			lastTargets, _ = fetchCDPTargets(port)
			fmt.Fprintf(os.Stderr, "idle-timeout: serve will exit after %s of inactivity\n", idleTimeout)
		}

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		missed := 0 // consecutive failed health probes
		for {
			select {
			case <-sig:
				fmt.Fprintln(os.Stderr, "\nshutting down")
				return
			case <-ticker.C:
				// If Chrome is gone, exit instead of lingering with no browser.
				//
				// But "one probe timed out" is not "Chrome is gone". This probe
				// used to be single-strike with a 1.5s deadline, and on a busy
				// or CPU-throttled host that killed healthy browsers: Chrome
				// rendering a heavy page simply answers /json/version late.
				// Observed on a 2-vCPU VM running a scraper — 3 064 teardowns
				// across the run history, none of them a real crash (no
				// segfault, no OOM in the kernel log). Each one aborted whatever
				// the client had in flight.
				//
				// So: a longer deadline, and a browser is only declared dead
				// after serveHealthStrikes consecutive failures — the same
				// reasoning as a container healthcheck's `retries`. A genuine
				// death still gets noticed, just ~30s later instead of 3s.
				if port > 0 {
					if _, err := engine.DiscoverCDP([]int{port}, serveProbeTimeout); err != nil {
						missed++
						if missed < serveHealthStrikes {
							fmt.Fprintf(os.Stderr,
								"chrome did not answer the health probe (%d/%d): %v\n",
								missed, serveHealthStrikes, err)
							continue
						}
						fmt.Fprintf(os.Stderr,
							"chrome no longer reachable after %d consecutive probes — serve exiting\n",
							missed)
						return
					}
					missed = 0
				}
				// Idle shutdown: the CDP target set (open tabs + their URLs)
				// changes whenever an agent navigates or opens/closes a tab. If
				// it hasn't changed for idleTimeout, the daemon is idle — exit.
				if idleTimeout > 0 && port > 0 {
					if cur, ok := fetchCDPTargets(port); ok {
						if cur != lastTargets {
							lastTargets = cur
							lastActivity = time.Now()
						} else if time.Since(lastActivity) >= idleTimeout {
							if flagUserProfile != "" && engine.SessionLeaseFresh(flagUserProfile, idleTimeout) {
								lastActivity = time.Now()
								continue
							}

							fmt.Fprintf(os.Stderr, "idle for %s — serve exiting\n", idleTimeout)
							return
						}
					}
				}
			}
		}
	},
}

func launchServeChrome(opts engine.LauncherOpts) (string, func(), error) {
	l := engine.NewLauncher(opts)
	removeProfile := engine.LauncherOwnsRodTempProfile(l, opts.UserDataDir, opts.Args)
	wsURL, err := l.Launch()
	if err != nil {
		engine.CleanupFailedLauncher(l, removeProfile)
		return "", nil, err
	}

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			engine.CleanupLauncher(l, removeProfile)
		})
	}
	return wsURL, cleanup, nil
}

var serveStealthWarmup = warmUpStealth

func warmUpServeChrome(wsURL string, cleanup func()) error {
	if err := serveStealthWarmup(wsURL); err != nil {
		cleanup()
		return err
	}
	return nil
}

// defaultServeIdleTimeout is the CLI daemon idle window when
// GHOSTCHROME_IDLE_TIMEOUT is unset. Named sessions spawned by the
// transparent daemon inherit this: attached/headed/leased Chrome is
// never reaped (serve does not own those processes).
const defaultServeIdleTimeout = time.Hour

// serveIdleTimeout parses GHOSTCHROME_IDLE_TIMEOUT. Empty falls back to
// 1h. Explicit 0/off/invalid disables idle shutdown. Accepts a Go duration
// ("30m", "90s") or a bare integer number of seconds.

// serveIdleTimeoutForSession applies ownership rules on top of the parsed
// idle window: headed Chrome is a live user session and is never reaped.
func serveIdleTimeoutForSession(headless bool) time.Duration {
	if !headless {
		return 0
	}
	return serveIdleTimeout()
}
func serveIdleTimeout() time.Duration {
	v := strings.TrimSpace(os.Getenv("GHOSTCHROME_IDLE_TIMEOUT"))
	if v == "" {
		return defaultServeIdleTimeout
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	fmt.Fprintf(os.Stderr, "ignoring invalid GHOSTCHROME_IDLE_TIMEOUT=%q\n", v)
	return 0
}

// fetchCDPTargets returns the raw /json/list body — a stable signature of the
// open targets and their URLs — used as a cheap activity fingerprint. The bool
// is false when the endpoint can't be read (treated as "no change").
func fetchCDPTargets(port int) (string, bool) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", false
	}
	return string(body), true
}

func warmUpStealth(wsURL string) error {
	b := rod.New().ControlURL(wsURL)
	if err := b.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	// NOTE: do NOT call b.Close() — that sends `Browser.close` to CDP and kills
	// the long-lived Chrome we just launched. The Rod connection cleans itself
	// up when the process exits.

	page, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		return fmt.Errorf("page: %w", err)
	}
	defer page.Close()

	return engine.ApplyStealth(page)
}

func init() {
	serveCmd.Flags().IntVar(&flagPort, "port", 0, "Chrome remote debugging port (0 = random)")
	rootCmd.AddCommand(serveCmd)
}
