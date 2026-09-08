package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	flagLoginSite       string
	flagLoginSuccessURL string
	flagLoginTimeoutMin int
	flagLoginManual     bool
)

type loginPreset struct {
	url        string
	successURL string
}

var loginPresets = map[string]loginPreset{
	"linkedin":  {url: "https://www.linkedin.com/login", successURL: "/feed"},
	"twitter":   {url: "https://x.com/login", successURL: "/home"},
	"x":         {url: "https://x.com/login", successURL: "/home"},
	"github":    {url: "https://github.com/login", successURL: "github.com"},
	"google":    {url: "https://accounts.google.com/", successURL: "myaccount.google.com"},
	"notion":    {url: "https://www.notion.so/login", successURL: "/workspace"},
	"slack":     {url: "https://slack.com/signin", successURL: "/messages"},
	"instagram": {url: "https://www.instagram.com/accounts/login/", successURL: "/accounts/onetap"},
	"leboncoin": {url: "https://www.leboncoin.fr/compte/part/login", successURL: ""},
}

var loginCmd = &cobra.Command{
	Use:   "login [site|url]",
	Short: "Open Chrome non-headless to login interactively, then save the session",
	Long: `Login launches a visible Chrome window on the persistent profile, navigates
to the login page, and waits until you complete authentication (manual typing,
2FA, captcha — whatever the site needs). Once it detects a successful login,
Chrome is closed gracefully via CDP so cookies/IndexedDB are flushed to disk.

The same Chrome binary is used for the login and for subsequent headless runs,
so the TLS fingerprint stays consistent — critical for sites that bind cookies
to a device fingerprint (LinkedIn, Cloudflare-protected sites).

Presets: linkedin, twitter/x, github, google, notion, slack, instagram, leboncoin. Or pass any URL.

Examples:
  ghostchrome --user-profile kev login linkedin
  ghostchrome --user-profile leboncoin login leboncoin
  ghostchrome --user-profile work login https://app.notion.so --success-url /workspace
  ghostchrome --user-profile kev login linkedin --manual    # press Enter when done`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target := flagLoginSite
		successURL := flagLoginSuccessURL
		if len(args) > 0 {
			target = args[0]
		}
		if target == "" {
			exitErr("login", fmt.Errorf("need a site preset or URL (e.g. linkedin, https://...)"))
		}

		if preset, ok := loginPresets[target]; ok {
			target = preset.url
			if successURL == "" {
				successURL = preset.successURL
			}
		}

		opts := buildBrowserOpts()
		if opts.UserDataDir == "" {
			exitErr("login", fmt.Errorf("--user-profile is required (cookies need a place to live)"))
		}
		if opts.ConnectURL != "" {
			exitErr("login", fmt.Errorf("login is for auto-launch only; remove --connect"))
		}

		opts.Headless = false
		// Force system Chrome.app so:
		//  1. The window registers with macOS as Google Chrome (visible in
		//     the Dock, focusable, displayed on the user's actual screen).
		//  2. Cookies written by Chrome.app (bundle com.google.Chrome) can
		//     be decrypted by future runs that also use Chrome.app — same
		//     Keychain entry "Chrome Safe Storage", same TLS fingerprint as
		//     the user's regular Chrome (critical for LinkedIn-style device
		//     binding).
		opts.SystemChrome = true
		if engine.FindSystemChromeBinary() == "" {
			fmt.Fprintln(os.Stderr, "warning: Google Chrome.app not found at /Applications/Google Chrome.app — falling back to bundled Chromium")
			opts.SystemChrome = false
		}
		b, err := engine.NewBrowserWith(opts)
		if err != nil {
			exitErr("login", err)
		}

		// Persist the marker so subsequent `ghostchrome --user-profile X ...`
		// runs also pick the system Chrome binary. Without this, headless
		// runs would launch bundled Chromium and the cookies (encrypted by
		// Chrome.app's Keychain key) would not decrypt.
		if opts.SystemChrome {
			marker := opts.UserDataDir + "/.ghostchrome-system-chrome"
			_ = os.WriteFile(marker, []byte("system\n"), 0o600)
		}

		var closed atomic.Bool
		gracefulClose := func(reason string) {
			if !closed.CompareAndSwap(false, true) {
				return
			}
			fmt.Fprintf(os.Stderr, "Closing Chrome gracefully (%s) — flushing cookies…\n", reason)
			_ = b.RodBrowser().Close()
			time.Sleep(1500 * time.Millisecond)
		}

		// Ctrl+C handler: do a graceful close so cookies hit the disk.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			gracefulClose("interrupted")
			os.Exit(0)
		}()

		page, err := b.Page()
		if err != nil {
			gracefulClose("page error")
			exitErr("login", err)
		}

		if err := page.Navigate(target); err != nil {
			gracefulClose("nav error")
			exitErr("login", err)
		}

		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintf(os.Stderr, "  Chrome opened on %s\n", target)
		fmt.Fprintf(os.Stderr, "  Login manually in the window that just appeared.\n")
		if flagLoginManual {
			fmt.Fprintln(os.Stderr, "  When you're done, come back here and press <Enter>.")
		} else if successURL != "" {
			fmt.Fprintf(os.Stderr, "  Auto-detect: success when URL contains %q\n", successURL)
			fmt.Fprintln(os.Stderr, "  Or press <Enter> here at any time to confirm manually.")
		} else {
			fmt.Fprintln(os.Stderr, "  Press <Enter> here when login is complete.")
		}
		fmt.Fprintf(os.Stderr, "  Timeout: %d min. Ctrl+C aborts (cookies still flushed).\n\n", flagLoginTimeoutMin)

		// Stdin watcher: any Enter triggers manual confirmation. Only
		// register it when stdin is an interactive terminal — otherwise
		// stdin returns EOF immediately and would kill the session before
		// the user can do anything.
		manualDone := make(chan struct{}, 1)
		stdinIsTTY := term.IsTerminal(int(os.Stdin.Fd()))
		if stdinIsTTY {
			go func() {
				r := bufio.NewReader(os.Stdin)
				_, _ = r.ReadString('\n')
				select {
				case manualDone <- struct{}{}:
				default:
				}
			}()
		} else if flagLoginManual {
			fmt.Fprintln(os.Stderr, "warning: --manual requires an interactive TTY; falling back to auto-detect")
			if successURL == "" {
				gracefulClose("non-interactive without success-url")
				exitErr("login", fmt.Errorf("non-interactive shell + --manual + no --success-url: cannot decide when login is done"))
			}
		}

		deadline := time.Now().Add(time.Duration(flagLoginTimeoutMin) * time.Minute)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		var lastURL string
		for {
			select {
			case <-manualDone:
				gracefulClose("manual confirm")
				fmt.Fprintf(os.Stderr, "Session saved to %s\n", opts.UserDataDir)
				return
			case <-ticker.C:
				if time.Now().After(deadline) {
					gracefulClose("timeout")
					exitErr("login", fmt.Errorf("timeout after %d min — login not detected", flagLoginTimeoutMin))
				}
				info, err := page.Info()
				if err != nil {
					// Likely the user closed the Chrome window themselves.
					gracefulClose("window closed")
					fmt.Fprintf(os.Stderr, "Session saved to %s\n", opts.UserDataDir)
					return
				}
				if info.URL != lastURL {
					fmt.Fprintf(os.Stderr, "  → %s\n", info.URL)
					lastURL = info.URL
				}
				if !flagLoginManual && successURL != "" && strings.Contains(info.URL, successURL) {
					fmt.Fprintf(os.Stderr, "Login detected (URL matched %q).\n", successURL)
					time.Sleep(3 * time.Second) // settle post-login XHRs
					gracefulClose("login detected")
					fmt.Fprintf(os.Stderr, "Session saved to %s\n", opts.UserDataDir)
					return
				}
			}
		}
	},
}

func init() {
	loginCmd.Flags().StringVar(&flagLoginSite, "site", "", "Login URL or preset (linkedin/twitter/github/google/notion/slack/instagram/leboncoin)")
	loginCmd.Flags().StringVar(&flagLoginSuccessURL, "success-url", "", "URL substring that signals login success (e.g. /feed/)")
	loginCmd.Flags().IntVar(&flagLoginTimeoutMin, "login-timeout-min", 10, "Minutes to wait for login completion")
	loginCmd.Flags().BoolVar(&flagLoginManual, "manual", false, "Disable auto-detect; only Enter key confirms login is done")
	rootCmd.AddCommand(loginCmd)
}
