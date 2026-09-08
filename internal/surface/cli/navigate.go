package cli

import (
	"fmt"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/spf13/cobra"
)

var (
	flagWait       string
	flagNavExtract string
)

var navigateCmd = &cobra.Command{
	Use:     "navigate <url>",
	Aliases: []string{"goto"},
	Short:   "Navigate to a URL and return page info",
	Long: `Navigate to a URL. Optionally extract the DOM in one shot with --extract.

Examples:
  ghostchrome navigate https://example.com
  ghostchrome navigate https://example.com --extract skeleton
  ghostchrome navigate https://example.com --extract content --format json`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		targetURL := args[0]
		isGoto := cmd.CalledAs() == "goto"

		b, page := openPage()
		defer b.Close()

		t0 := time.Now()
		flush := startObserveOut(page)

		info := navigateIfRequested(page, targetURL, flagWait)
		durationMs := time.Since(t0).Milliseconds()

		// If --extract is set, also extract DOM
		if flagNavExtract != "" {
			level := engine.ExtractLevel(flagNavExtract)
			result := snapshotPage(b, page, level, consumeNavChallengeRecovered())
			text := fmt.Sprintf("[%d] %s — %s (%dms)\n%s", info.Status, info.Title, info.URL, info.TimeMs, engine.FormatTextProfile(result, renderProfile()))
			text = appendAutoPlaywrightSnapshot(text, result)
			flush("navigate", true, durationMs, fmt.Sprintf("[%d] %s", info.Status, info.URL))
			type navigateExtractResult struct {
				*engine.PageInfo
				DOM *engine.ExtractionResult `json:"dom"`
			}
			output(&navigateExtractResult{info, result}, text)
			return
		}

		if isGoto {
			result := snapshotPage(b, page, engine.LevelSkeleton, consumeNavChallengeRecovered())
			text := formatPlaywrightPageStateOutput(info, result)
			flush("goto", true, durationMs, fmt.Sprintf("[%d] %s", info.Status, info.URL))
			type gotoResult struct {
				*engine.PageInfo
				Snapshot *engine.ExtractionResult `json:"snapshot,omitempty"`
			}
			output(&gotoResult{PageInfo: info, Snapshot: result}, text)
			return
		}

		// Best-effort snapshot for later ref-based commands (click, type).
		// On heavy pages the extraction can time out after a long navigation;
		// that must not fail navigate itself — the user can always extract later.
		trySnapshot(b, page, engine.LevelSkeleton)
		text := fmt.Sprintf("[%d] %s — %s (%dms)", info.Status, info.Title, info.URL, info.TimeMs)
		flush("navigate", true, durationMs, fmt.Sprintf("[%d] %s", info.Status, info.URL))
		output(info, text)
	},
}

func init() {
	navigateCmd.Flags().StringVar(&flagWait, "wait", "load", "Wait strategy: load, stable, idle, none")
	navigateCmd.Flags().StringVar(&flagNavExtract, "extract", "", "Also extract DOM: skeleton, content, or full")
	rootCmd.AddCommand(navigateCmd)
}
