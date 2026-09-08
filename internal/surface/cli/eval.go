package cli

import (
	"errors"
	"io"
	"os"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

var (
	flagOnRef      string
	flagScriptFile string
)

var errMissingExpr = errors.New("expression required (provide as arg or via --script <file>)")

var evalCmd = &cobra.Command{
	Use:   "eval [expression] [url|ref]",
	Short: "Evaluate JavaScript on the page",
	Long: `Evaluate a JavaScript expression on the page and return the result.
If a URL is provided, navigates first then evaluates.
Use --on @ref to evaluate in the context of a specific element.
Use --script <file> ('-' for stdin) to load the JS body from a file —
useful for long extractors where shell-quoting becomes unwieldy.

Examples:
  ghostchrome eval "document.title" https://example.com
  ghostchrome eval "this.textContent" --on @1 --connect ws://...
  ghostchrome eval --script extract.js https://example.com
  cat extract.js | ghostchrome eval --script - https://example.com`,
	Args: cobra.RangeArgs(0, 2),
	Run: func(cmd *cobra.Command, args []string) {
		var expr string
		urlArgIdx := 0
		if flagScriptFile != "" {
			var data []byte
			var err error
			if flagScriptFile == "-" {
				data, err = io.ReadAll(os.Stdin)
			} else {
				data, err = os.ReadFile(flagScriptFile)
			}
			if err != nil {
				exitErr("eval", err)
			}
			expr = string(data)
		} else {
			if len(args) < 1 {
				exitErr("eval", errMissingExpr)
			}
			expr = args[0]
			urlArgIdx = 1
		}
		targetURL := ""
		if len(args) > urlArgIdx {
			if isSnapshotRef(args[urlArgIdx]) || !looksLikeURL(args[urlArgIdx]) {
				if flagOnRef != "" && flagOnRef != args[urlArgIdx] {
					exitErr("eval", errors.New("element ref specified twice"))
				}
				flagOnRef = args[urlArgIdx]
			} else {
				targetURL = args[urlArgIdx]
			}
		}

		b, page := openPage()
		defer b.Close()

		var snapshot *engine.PageSnapshot
		if isSnapshotRef(flagOnRef) || targetURL != "" {
			snapshot = ensureSnapshot(b, page, targetURL, "load", engine.LevelSkeleton)
		}

		result, err := engine.EvalJS(page, expr, flagOnRef, snapshot)
		if err != nil {
			exitIfStaleRef(err, "eval")
			exitErr("eval", err)
		}
		_ = b.InvalidateCachedExtract(page)

		type evalResult struct {
			Expression string `json:"expression"`
			Result     string `json:"result"`
		}

		output(&evalResult{
			Expression: expr,
			Result:     result,
		}, result)
	},
}

func init() {
	evalCmd.Flags().StringVar(&flagOnRef, "on", "", "Evaluate in context of element @ref")
	evalCmd.Flags().StringVar(&flagScriptFile, "script", "", "Read JS body from file ('-' for stdin)")
	rootCmd.AddCommand(evalCmd)
}
