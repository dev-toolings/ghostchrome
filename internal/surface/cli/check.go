package cli

import (
	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/spf13/cobra"
)

func runCheck(action string, checked bool) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, args []string) {
		ref := args[0]
		targetURL := ""
		if len(args) > 1 {
			targetURL = args[1]
		}

		b, page := openPage()
		defer b.Close()

		snapshot := ensureSnapshot(b, page, targetURL, "load", engine.LevelSkeleton)

		if err := engine.SetCheckedRef(page, ref, checked, snapshot); err != nil {
			exitIfStaleRef(err, action)
			exitErr(action, err)
		}

		emitMutationOutput(action, ref, b, page, nil)
	}
}

var checkCmd = &cobra.Command{
	Use:   "check [ref] [url]",
	Short: "Check a checkbox or radio by ref (idempotent)",
	Long: `Tick a checkbox or radio identified by its @ref. Idempotent: if the box
is already checked, nothing happens (no accidental untick).

Examples:
  ghostchrome check @7 --connect ws://127.0.0.1:9222`,
	Args: cobra.RangeArgs(1, 2),
	Run:  runCheck("check", true),
}

var uncheckCmd = &cobra.Command{
	Use:   "uncheck [ref] [url]",
	Short: "Uncheck a checkbox by ref (idempotent)",
	Long: `Untick a checkbox identified by its @ref. Idempotent: if the box is
already unchecked, nothing happens.

Examples:
  ghostchrome uncheck @7 --connect ws://127.0.0.1:9222`,
	Args: cobra.RangeArgs(1, 2),
	Run:  runCheck("uncheck", false),
}

func init() {
	registerSnapshotModeFlag(checkCmd)
	rootCmd.AddCommand(checkCmd)
	registerSnapshotModeFlag(uncheckCmd)
	rootCmd.AddCommand(uncheckCmd)
}
