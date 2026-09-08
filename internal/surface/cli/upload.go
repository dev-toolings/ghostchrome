package cli

import (
	"fmt"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

var flagUploadSelector string

var uploadCmd = &cobra.Command{
	Use:   "upload [ref] <file> [file2 ...]",
	Short: "Attach one or more files to a file-input element",
	Long: `upload attaches files to a <input type="file"> element. Most sites hide
the input behind a styled button, so provide either:

  - a @ref (works if the accessibility tree exposes the native input); or
  - --selector "input[type=file]" (the reliable path — works even when the
    button is a fancy wrapper around a hidden input).

Paths are resolved to absolute before DOM.setFileInputFiles is dispatched.

Examples:
  ghostchrome upload @3 /path/to/avatar.png
  ghostchrome upload --selector 'input[type=file]' ./cv.pdf --connect ws://...
  ghostchrome upload --selector '#avatar' a.png b.png c.png`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ref := ""
		files := args
		if flagUploadSelector == "" {
			if len(args) < 2 {
				exitErr("upload", fmt.Errorf("need REF and at least one FILE (or --selector + FILE)"))
			}
			if !strings.HasPrefix(args[0], "@") {
				exitErr("upload", fmt.Errorf("first arg must be @ref or use --selector"))
			}
			ref = args[0]
			files = args[1:]
		}

		b, page := openPage()
		defer b.Close()

		snapshot := ensureSnapshot(b, page, "", "none", engine.LevelSkeleton)

		if err := engine.UploadRef(page, ref, flagUploadSelector, files, snapshot); err != nil {
			exitIfStaleRef(err, "upload")
			exitErr("upload", err)
		}

		emitMutationOutput("upload", ref, b, page, nil)
	},
}

func init() {
	uploadCmd.Flags().StringVar(&flagUploadSelector, "selector", "", "CSS selector for the file input (use when the ref is a styled wrapper button)")
	registerSnapshotModeFlag(uploadCmd)
	rootCmd.AddCommand(uploadCmd)
}
