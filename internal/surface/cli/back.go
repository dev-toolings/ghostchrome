package cli

import (
	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

type navResult struct {
	Action string `json:"action"`
	URL    string `json:"url"`
	Title  string `json:"title"`
}

func runHistoryStep(action string) {
	b, page := openPage()
	defer b.Close()

	delta := 1
	if action == "back" {
		delta = -1
	}
	if err := engine.HistoryStep(page, delta, "load"); err != nil {
		exitErr(action, err)
	}

	info, err := page.Info()
	if err != nil {
		exitErr("page info", err)
	}

	result := snapshotPageAfterMutation(b, page, engine.LevelSkeleton)

	text := formatPlaywrightPageStateOutput(&engine.PageInfo{
		URL:   info.URL,
		Title: info.Title,
	}, result)
	output(&navResult{
		Action: action,
		URL:    info.URL,
		Title:  info.Title,
	}, text)
}

var backCmd = &cobra.Command{
	Use:     "back",
	Aliases: []string{"go-back"},
	Short:   "Navigate back in browser history",
	Long: `Go back one step in the current page's navigation history.
Returns the resulting page URL and title after navigating back.

Examples:
  ghostchrome back --connect ws://127.0.0.1:9222`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		runHistoryStep("back")
	},
}

var forwardCmd = &cobra.Command{
	Use:     "forward",
	Aliases: []string{"go-forward"},
	Short:   "Navigate forward in browser history",
	Long: `Go forward one step in the current page's navigation history.
Returns the resulting page URL and title after navigating forward.

Examples:
  ghostchrome forward --connect ws://127.0.0.1:9222`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		runHistoryStep("forward")
	},
}

func init() {
	rootCmd.AddCommand(backCmd)
	rootCmd.AddCommand(forwardCmd)
}
