package cli

import (
	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/spf13/cobra"
)

var hoverLocator LocatorFlags
var hoverWaitFor string
var hoverWaitTimeoutMs int

var hoverCmd = &cobra.Command{
	Use:   "hover [ref|url]",
	Short: "Hover over an element by ref or semantic locator",
	Long: `Hover over an element identified by @ref or --by-role / --by-name /
--by-label / --by-text. If a URL is provided, navigates first.

Examples:
  ghostchrome hover @2 --connect ws://...
  ghostchrome hover --by-role link --by-name "Docs"`,
	Args: cobra.RangeArgs(0, 2),
	Run: func(cmd *cobra.Command, args []string) {
		ref := ""
		targetURL := ""
		if !hoverLocator.Any() {
			if len(args) == 0 {
				exitErr("hover", errNeedRefOrLocator())
			}
			ref = args[0]
			if len(args) > 1 {
				targetURL = args[1]
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

		waitState, waitTimeout := resolveWaitFlags(cmd, hoverWaitFor, hoverWaitTimeoutMs)

		if hoverLocator.Any() {
			el, err := engine.WaitForLocator(page, hoverLocator.ToLocator(), waitState, waitTimeout)
			if err != nil {
				exitErr("hover", err)
			}
			if err := engine.HoverElement(page, el); err != nil {
				exitErr("hover", err)
			}
		} else {
			if waitTimeout > 0 {
				if _, err := engine.WaitForTarget(page, ref, snapshot, waitState, waitTimeout); err != nil {
					exitIfStaleRef(err, "hover")
					exitErr("hover", err)
				}
			}
			if err := engine.HoverRef(page, ref, snapshot); err != nil {
				exitIfStaleRef(err, "hover")
				exitErr("hover", err)
			}
		}

		emitMutationOutput("hover", ref, b, page, nil)
	},
}

func init() {
	hoverLocator.RegisterOn(hoverCmd)
	hoverCmd.Flags().StringVar(&hoverWaitFor, "wait-for", "", "Wait for element state before hovering: attached|visible|hidden|enabled|stable|none (default: visible)")
	hoverCmd.Flags().IntVar(&hoverWaitTimeoutMs, "wait-timeout-ms", 0, "Max milliseconds to wait for the element state (0 = no wait; default: 5000)")
	registerSnapshotModeFlag(hoverCmd)
	rootCmd.AddCommand(hoverCmd)
}
