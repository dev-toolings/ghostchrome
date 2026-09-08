package cli

import (
	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

var flagDragSteps int

var dragCmd = &cobra.Command{
	Use:   "drag <from-ref> <to-ref>",
	Short: "Drag an element to another element",
	Long: `Drag and drop from one element to another using mouse events.
Refs come from the last extract/preview/navigate snapshot.

Examples:
  ghostchrome drag @1 @5
  ghostchrome drag @3 @7 --steps 20`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		fromRef := args[0]
		toRef := args[1]

		b, page := openPage()
		defer b.Close()

		snapshot := ensureSnapshot(b, page, "", "load", engine.LevelSkeleton)

		if err := engine.DragDrop(page, fromRef, toRef, snapshot, flagDragSteps); err != nil {
			exitIfStaleRef(err, "drag")
			exitErr("drag", err)
		}

		emitMutationOutput("drag", fromRef, b, page, nil)
	},
}

func init() {
	dragCmd.Flags().IntVar(&flagDragSteps, "steps", 10, "Number of intermediate mouse move steps")
	registerSnapshotModeFlag(dragCmd)
	rootCmd.AddCommand(dragCmd)
}
