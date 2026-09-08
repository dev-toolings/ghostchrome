package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	mcpsurface "github.com/dev-toolings/ghostchrome/internal/surface/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

// defaultMCPIdleTimeout is the reap-after-inactivity window used when
// GHOSTCHROME_IDLE_TIMEOUT is unset. Unlike the serve daemon (opt-in, stays
// persistent per the runtime policy), the stdio MCP server is spawned once per
// agent session and often outlives real usage by hours — so it defaults to
// releasing idle Chrome to avoid forgotten sessions squatting ~600MB each. The
// server stays up and relaunches Chrome on the next call, so the only cost is a
// ~1s cold start after a long pause.
const defaultMCPIdleTimeout = 15 * time.Minute

// mcpIdleTimeout resolves the idle-reap window for the MCP server. Empty env
// falls back to defaultMCPIdleTimeout (reaping on by default here); an explicit
// value is honoured verbatim, and "0"/"off"/invalid disables reaping entirely.
func mcpIdleTimeout() time.Duration {
	if strings.TrimSpace(os.Getenv("GHOSTCHROME_IDLE_TIMEOUT")) == "" {
		return defaultMCPIdleTimeout
	}
	return serveIdleTimeout() // parses the value; 0/off/invalid -> disabled
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run ghostchrome as a Model Context Protocol server (stdio)",
	Long: `Start an MCP server that exposes the ghostchrome browser to LLM agents
(Claude Code, Codex, Cursor, ...) over JSON-RPC on stdin/stdout.

The server holds one long-lived Chrome + page across tool calls so refs
(@1, @2, ...) extracted by one tool stay valid for the next. Honours the
same global flags as the regular CLI: --connect, --headless, --user-profile,
--stealth, --dismiss-cookies, --proxy, --timeout.

Idle Chrome is auto-released after GHOSTCHROME_IDLE_TIMEOUT of no tool activity
(default 15m; e.g. "30m" or "900") — the server stays up and relaunches Chrome
on the next call, so a forgotten-but-open session stops squatting idle memory.
Set GHOSTCHROME_IDLE_TIMEOUT=0 to disable and keep Chrome pinned for the whole
session.

Wire-up (one-time):

  # Claude Code
  claude mcp add ghostchrome -- ghostchrome mcp --stealth

  # Codex
  codex mcp add ghostchrome -- ghostchrome mcp --stealth

The server speaks MCP 2025-11-25 and exposes 16 tools:

  snapshot       page status + errors + network + DOM with refs (canonical first call)
  navigate       go to URL without snapshot
  click          click @ref
  type           type text into @ref (with submit=true to press Enter)
  select         pick option in <select> by @ref
  press          send a key (Enter, Tab, Escape, ...)
  hover          hover an element by @ref
  drag           drag from one @ref to another
  fill_form      bulk-fill form fields from JSON {ref: value}
  upload         attach files to <input type=file> by @ref
  tabs           list / switch / close browser tabs
  wait_for       wait for selector / text / timeout
  eval           run JS, escape hatch for anything else
  screenshot     WebP/JPEG/PNG of viewport, full page, or element (with annotate)
  back / forward browser history`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if flagSession == "" {
			skipImplicitDaemon = true
		} else if flagConnect == "" {
			ws, err := engine.AcquireSession(flagSession, engine.SessionSpawnOpts{
				Headless: flagHeadless,
				Stealth:  flagStealth,
				Proxy:    flagProxy,
			})
			if err != nil {
				exitErr("session", err)
			}
			flagConnect = ws
			engine.TouchSessionLease(flagSession)
		}
		opts := mcpsurface.Options{
			Connect:        flagConnect,
			Headless:       flagHeadless,
			Invisible:      flagInvisible,
			UserProfile:    flagUserProfile,
			Stealth:        flagStealth,
			DismissCookies: flagDismissCookies,
			Proxy:          flagProxy,
			TimeoutSec:     flagTimeout,
			BlockTrackers:  flagMCPBlockTrackers,
			Policy:         engine.ActivePolicy,
			SessionName:    flagSession,
			// Same GHOSTCHROME_IDLE_TIMEOUT knob as `serve`, but here it reaps
			// only the held browser (the stdio server stays up and relaunches
			// Chrome on demand) instead of exiting the process — and it is ON by
			// default (15m) so idle sessions can't squat memory indefinitely.
			IdleTimeout: mcpIdleTimeout(),
		}
		s := mcpsurface.New(opts)
		defer s.Close()
		s.StartIdleReaper()

		// Pre-spawn Chrome in the background while the MCP client is still
		// negotiating capabilities. The 1-2s cold start is hidden from the
		// first user-facing tool call. Lazy mode disabled via GHOSTCHROME_MCP_LAZY=1.
		if os.Getenv("GHOSTCHROME_MCP_LAZY") != "1" {
			s.PrewarmAsync()
		}

		fmt.Fprintln(os.Stderr, "[ghostchrome mcp] ready on stdio (prewarm in background)")
		// SIGTERM/SIGINT surface as context.Canceled from ServeStdio: that is a
		// normal shutdown, not an error. Close Chrome gracefully in both cases —
		// os.Exit would skip the defer and leave the kill to leakless.
		if err := mcpsrv.ServeStdio(s.Build("ghostchrome", rootCmd.Version)); err != nil && !errors.Is(err, context.Canceled) {
			s.Close()
			fmt.Fprintf(os.Stderr, "mcp server: %v\n", err)
			os.Exit(1)
		}
	},
}

var flagMCPBlockTrackers bool

func init() {
	mcpCmd.Flags().BoolVar(&flagMCPBlockTrackers, "block-trackers", false,
		"Block known anti-bot/fingerprint scripts (DataDome, PerimeterX, ...) at the network layer. Auto-on with --stealth.")
	rootCmd.AddCommand(mcpCmd)
}
