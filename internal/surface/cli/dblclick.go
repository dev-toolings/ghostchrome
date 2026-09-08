package cli

import (
	"fmt"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/spf13/cobra"
)

var dblclickCmd = &cobra.Command{
	Use:   "dblclick [ref] [button|url]",
	Short: "Double-click an interactive element by ref",
	Long: `Double-click the element identified by its @ref (from the last snapshot).
If a second argument is provided and matches left|right|middle, it is used as
the mouse button. Otherwise it is treated as a URL to navigate before
double-clicking.
After double-clicking, prints a compact a11y-ref diff (override with --snapshot=full|none).

Examples:
  ghostchrome dblclick @3 --connect ws://127.0.0.1:9222
  ghostchrome dblclick @1 https://example.com
  ghostchrome dblclick @1 right`,
	Args: cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		ref := args[0]
		targetURL := ""
		button, hasButton := parseMouseButtonStrict("left")
		if !hasButton {
			exitErr("dblclick", fmt.Errorf("unexpected default button state"))
		}
		if len(args) > 1 {
			if b, ok := parseMouseButtonStrict(args[1]); ok {
				button = b
			} else {
				targetURL = args[1]
			}
		}

		b, page := openPage()
		defer b.Close()

		snapshot := ensureSnapshot(b, page, targetURL, "load", engine.LevelSkeleton)

		if err := engine.DblClickRefWithButton(page, ref, snapshot, button); err != nil {
			exitIfStaleRef(err, "dblclick")
			exitErr("dblclick", err)
		}

		emitMutationOutput("dblclick", ref, b, page, nil)
	},
}

func init() {
	registerSnapshotModeFlag(dblclickCmd)
	rootCmd.AddCommand(dblclickCmd)
}
