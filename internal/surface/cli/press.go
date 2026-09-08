package cli

import (
	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

var flagPressOn string
var pressWaitFor string
var pressWaitTimeoutMs int

var pressCmd = &cobra.Command{
	Use:   "press <key> [url]",
	Short: "Press a keyboard key",
	Long: `Press a keyboard key (Enter, Tab, Escape, Backspace, ArrowDown, etc.).
If a URL is provided, navigates first then presses.
Use --on @ref to focus an element before pressing.
After pressing, prints a compact a11y-ref diff (override with --snapshot=full|none).

Examples:
  ghostchrome press Enter https://example.com --on @2
  ghostchrome press Tab
  ghostchrome press Escape --connect ws://...`,
	Args: cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		targetURL := ""
		if len(args) > 1 {
			targetURL = args[1]
		}

		b, page := openPage()
		defer b.Close()

		snapshot := ensureSnapshot(b, page, targetURL, "load", engine.LevelSkeleton)

		// If --on is set, auto-wait for the element before pressing.
		if flagPressOn != "" {
			waitState, waitTimeout := resolveWaitFlags(cmd, pressWaitFor, pressWaitTimeoutMs)
			if waitTimeout > 0 {
				if _, err := engine.WaitForRef(page, flagPressOn, snapshot, waitState, waitTimeout); err != nil {
					exitIfStaleRef(err, "press")
					exitErr("press", err)
				}
			}
		}
		err := engine.PressKey(page, key, flagPressOn, snapshot)
		if err != nil {
			exitIfStaleRef(err, "press")
			exitErr("press", err)
		}

		emitMutationOutput("press", flagPressOn, b, page, nil)
	},
}

func init() {
	pressCmd.Flags().StringVar(&flagPressOn, "on", "", "Focus element by @ref before pressing (e.g. @2)")
	pressCmd.Flags().StringVar(&pressWaitFor, "wait-for", "", "Wait for --on element state before pressing: attached|visible|hidden|enabled|stable|none (default: visible)")
	pressCmd.Flags().IntVar(&pressWaitTimeoutMs, "wait-timeout-ms", 0, "Max milliseconds to wait for the element state (0 = no wait; default: 5000)")
	registerSnapshotModeFlag(pressCmd)
	rootCmd.AddCommand(pressCmd)
}
