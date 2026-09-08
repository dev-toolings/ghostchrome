package cli

import (
	"strings"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/spf13/cobra"
)

var flagSelectValues string
var selectWaitFor string
var selectWaitTimeoutMs int

var selectCmd = &cobra.Command{
	Use:   "select <ref> <value> [url]",
	Short: "Select option(s) in a <select> element by ref",
	Long: `Select one or more options in a <select> element identified by its @ref.
If a URL is provided, navigates first then selects.
Use --values "a,b,c" for multi-select.
After selecting, prints a compact a11y-ref diff (override with --snapshot=full|none).

Examples:
  ghostchrome select @5 "option1" https://example.com
  ghostchrome select @5 "" --values "a,b,c" https://example.com`,
	Args: cobra.RangeArgs(2, 3),
	Run: func(cmd *cobra.Command, args []string) {
		ref := args[0]

		var values []string
		if flagSelectValues != "" {
			values = strings.Split(flagSelectValues, ",")
		} else {
			values = []string{args[1]}
		}
		targetURL := ""
		if len(args) > 2 {
			targetURL = args[2]
		}

		b, page := openPage()
		defer b.Close()

		snapshot := ensureSnapshot(b, page, targetURL, "load", engine.LevelSkeleton)

		waitState, waitTimeout := resolveWaitFlags(cmd, selectWaitFor, selectWaitTimeoutMs)
		if waitTimeout > 0 {
			if _, err := engine.WaitForRef(page, ref, snapshot, waitState, waitTimeout); err != nil {
				exitIfStaleRef(err, "select")
				exitErr("select", err)
			}
		}
		err := engine.SelectOption(page, ref, values, snapshot)
		if err != nil {
			exitIfStaleRef(err, "select")
			exitErr("select", err)
		}

		emitMutationOutput("select", ref, b, page, nil)
	},
}

func init() {
	selectCmd.Flags().StringVar(&flagSelectValues, "values", "", "Comma-separated values for multi-select")
	selectCmd.Flags().StringVar(&selectWaitFor, "wait-for", "", "Wait for element state before selecting: attached|visible|hidden|enabled|stable|none (default: visible)")
	selectCmd.Flags().IntVar(&selectWaitTimeoutMs, "wait-timeout-ms", 0, "Max milliseconds to wait for the element state (0 = no wait; default: 5000)")
	registerSnapshotModeFlag(selectCmd)
	rootCmd.AddCommand(selectCmd)
}
