package cli

import (
	"fmt"
	"strconv"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/spf13/cobra"
)

var tabsCmd = &cobra.Command{
	Use:   "tabs",
	Short: "List open browser tabs",
	Long: `List all open tabs in the browser with their index, title, and URL.
Use the reported index with page commands via --tab when connected to a shared browser session.

Examples:
  ghostchrome tabs --connect ws://127.0.0.1:9222
  ghostchrome preview --connect ws://127.0.0.1:9222 --tab 1`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		b, _ := openPage()
		defer b.Close()

		browser := b.RodBrowser()
		tabs, err := engine.ListTabs(browser, b.CurrentTargetID())
		if err != nil {
			exitErr("list tabs", err)
		}

		type tabsResult struct {
			Tabs []engine.TabInfo `json:"tabs"`
		}

		var text string
		for i, t := range tabs {
			prefix := ""
			if t.Active {
				prefix = "*active* "
			}
			text += fmt.Sprintf("[%d] %s%s — %s\n", i, prefix, t.Title, t.URL)
		}

		output(&tabsResult{Tabs: tabs}, text)
	},
}

var tabsSwitchCmd = &cobra.Command{
	Use:   "switch <index>",
	Short: "Switch to a tab by index",
	Long: `Activate the tab at the given index.

Examples:
  ghostchrome tabs switch 2 --connect ws://127.0.0.1:9222`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		idx, err := strconv.Atoi(args[0])
		if err != nil {
			exitErr("parse index", err)
		}

		b, _ := openPage()
		defer b.Close()

		browser := b.RodBrowser()
		page, err := engine.SwitchTab(browser, idx)
		if err != nil {
			exitErr("switch tab", err)
		}
		if err := b.SetCurrentPage(page); err != nil {
			exitErr("switch tab", err)
		}

		result := snapshotPage(b, page, engine.LevelSkeleton)

		type switchResult struct {
			Action string                   `json:"action"`
			Index  int                      `json:"index"`
			Result *engine.ExtractionResult `json:"result,omitempty"`
		}

		text := formatCurrentPlaywrightPageStateOutput("tab-select", page, result)
		output(&switchResult{Action: "switch", Index: idx, Result: result}, text)
	},
}

var tabsCloseCmd = &cobra.Command{
	Use:   "close <index>",
	Short: "Close a tab by index",
	Long: `Close the tab at the given index.

Examples:
  ghostchrome tabs close 1 --connect ws://127.0.0.1:9222`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		idx, err := strconv.Atoi(args[0])
		if err != nil {
			exitErr("parse index", err)
		}

		b, _ := openPage()
		defer b.Close()

		browser := b.RodBrowser()
		targetID, err := engine.CloseTab(browser, idx)
		if err != nil {
			exitErr("close tab", err)
		}
		if err := b.DeleteSnapshot(targetID); err != nil {
			exitErr("close tab", err)
		}

		type closeResult struct {
			Action string `json:"action"`
			Index  int    `json:"index"`
		}

		text := fmt.Sprintf("Closed tab %d", idx)
		output(&closeResult{Action: "close", Index: idx}, text)
	},
}

var tabsNewCmd = &cobra.Command{
	Use:   "new [url]",
	Short: "Open a new tab (optionally navigating to a URL)",
	Long: `Open a new tab and activate it. If a URL is given, navigate there;
otherwise the tab opens on about:blank.

Examples:
  ghostchrome tabs new --connect ws://127.0.0.1:9222
  ghostchrome tabs new https://example.com --connect ws://127.0.0.1:9222`,
	Args: cobra.RangeArgs(0, 1),
	Run: func(cmd *cobra.Command, args []string) {
		url := ""
		if len(args) > 0 {
			url = args[0]
		}

		b, _ := openPage()
		defer b.Close()

		browser := b.RodBrowser()
		page, err := engine.NewTab(browser, url)
		if err != nil {
			exitErr("new tab", err)
		}
		if err := b.SetCurrentPage(page); err != nil {
			exitErr("new tab", err)
		}

		result := snapshotPage(b, page, engine.LevelSkeleton)

		info, _ := page.Info()
		type newResult struct {
			Action string                   `json:"action"`
			URL    string                   `json:"url"`
			Title  string                   `json:"title"`
			Result *engine.ExtractionResult `json:"result,omitempty"`
		}
		res := &newResult{Action: "new-tab", Result: result}
		if info != nil {
			res.URL = info.URL
			res.Title = info.Title
		}
		text := formatCurrentPlaywrightPageStateOutput("tab-new", page, result)
		output(res, text)
	},
}

func init() {
	tabsCmd.AddCommand(tabsSwitchCmd)
	tabsCmd.AddCommand(tabsCloseCmd)
	tabsCmd.AddCommand(tabsNewCmd)
	rootCmd.AddCommand(tabsCmd)
}
