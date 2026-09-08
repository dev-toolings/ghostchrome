package cli

import (
	"fmt"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

var clickLocator LocatorFlags
var clickWaitFor string
var clickWaitTimeoutMs int

var clickCmd = &cobra.Command{
	Use:   "click [ref|url] [button|url]",
	Short: "Click an interactive element by ref or semantic locator",
	Long: `Click an element identified by:
  - its @ref (from the last snapshot), OR
  - a semantic locator via --by-role / --by-name / --by-label / --by-text.

If a second argument is provided, it is interpreted as a mouse button when it is
left|right|middle. Otherwise it is treated as a URL and navigated first.
After clicking, prints a compact a11y-ref diff (override with --snapshot=full|none).

Examples:
  ghostchrome click @3 --connect ws://...
  ghostchrome click --by-role button --by-name "Sign in"
  ghostchrome click --by-text "Learn more"
  ghostchrome click @1 https://example.com
  ghostchrome click @3 right
  ghostchrome click --by-role button --by-name "Accept" --wait-for=visible --wait-timeout-ms=5000`,
	Args: cobra.RangeArgs(0, 2),
	Run: func(cmd *cobra.Command, args []string) {
		ref := ""
		targetURL := ""
		button, hasButton := parseMouseButtonStrict("left")
		if !hasButton {
			exitErr("click", fmt.Errorf("unexpected default button state"))
		}
		if !clickLocator.Any() {
			if len(args) == 0 {
				exitErr("click", errNeedRefOrLocator())
			}
			ref = args[0]
			if len(args) > 1 {
				if b, ok := parseMouseButtonStrict(args[1]); ok {
					button = b
				} else {
					targetURL = args[1]
				}
			}
		} else if len(args) > 0 {
			targetURL = args[0]
		}

		b, page := openPage()
		defer b.Close()

		var snapshot *engine.PageSnapshot
		if targetURL != "" || isSnapshotRef(ref) {
			snapshot = ensureSnapshot(b, page, targetURL, "load", engine.LevelSkeleton)
		}

		waitState, waitTimeout := resolveWaitFlags(cmd, clickWaitFor, clickWaitTimeoutMs)

		if clickLocator.Any() {
			el, err := engine.WaitForLocator(page, clickLocator.ToLocator(), waitState, waitTimeout)
			if err != nil {
				exitErr("click", err)
			}
			if err := engine.ClickElementWithButton(page, el, button); err != nil {
				exitErr("click", err)
			}
		} else {
			if waitTimeout > 0 {
				if _, err := engine.WaitForTarget(page, ref, snapshot, waitState, waitTimeout); err != nil {
					exitIfStaleRef(err, "click")
					exitErr("click", err)
				}
			}
			if err := engine.ClickRefWithButton(page, ref, snapshot, button); err != nil {
				exitIfStaleRef(err, "click")
				exitErr("click", err)
			}
		}

		emitMutationOutput("click", ref, b, page, nil)
	},
}

// resolveWaitFlags parses --wait-for / --wait-timeout-ms into typed values.
//
// Default behavior (when neither flag is explicitly set by the user):
//   - state  = visible
//   - timeout = 5000 ms
//
// Opt-out: pass --wait-for=none or --wait-timeout-ms=0 to disable auto-wait.
func resolveWaitFlags(cmd *cobra.Command, stateStr string, timeoutMs int) (engine.ElementState, time.Duration) {
	waitForChanged := cmd.Flags().Changed("wait-for")
	timeoutChanged := cmd.Flags().Changed("wait-timeout-ms")

	// Explicit opt-out: none state or zero timeout.
	if (waitForChanged && stateStr == "none") || (timeoutChanged && timeoutMs == 0) {
		return engine.StateAttached, 0
	}

	// Apply defaults when the user did not set either flag.
	if !waitForChanged && stateStr == "" {
		stateStr = "visible"
	}
	if !timeoutChanged && timeoutMs == 0 {
		timeoutMs = 5000
	}

	state, err := engine.ParseElementState(stateStr)
	if err != nil {
		exitErr("wait-for", err)
	}
	return state, time.Duration(timeoutMs) * time.Millisecond
}

func init() {
	clickLocator.RegisterOn(clickCmd)
	clickCmd.Flags().StringVar(&clickWaitFor, "wait-for", "", "Wait for element state before clicking: attached|visible|hidden|enabled|stable|none (default: visible)")
	clickCmd.Flags().IntVar(&clickWaitTimeoutMs, "wait-timeout-ms", 0, "Max milliseconds to wait for the element state (0 = no wait; default: 5000)")
	registerSnapshotModeFlag(clickCmd)
	rootCmd.AddCommand(clickCmd)
}
