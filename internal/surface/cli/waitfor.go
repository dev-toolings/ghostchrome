package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/go-rod/rod"
	"github.com/spf13/cobra"
)

var (
	flagWaitforTimeout      int
	flagWaitforSelector     string
	flagWaitforURLContains  string
	flagWaitforTitleContain string
	flagWaitforCountSel     string
	flagWaitforCountMin     int
	flagWaitforCountMax     int
)

var waitforCmd = &cobra.Command{
	Use:   "waitfor [css-selector] [url]",
	Short: "Wait for a page condition (selector, URL change, title, element count)",
	Long: `Wait for one of several conditions. At least one of the following must
be specified:

  --selector <css>      Element matching the selector appears (default mode)
  --url-contains <s>    Current page URL contains substring (useful after OAuth
                        redirects, SPA navigation, login flows)
  --title-contains <s>  document.title contains substring
  --count <selector>    Element count for selector satisfies --min/--max bounds

The positional <css-selector> is an alias for --selector (backward compatible).
An optional [url] positional navigates first, then waits.

Examples:
  ghostchrome waitfor "#results"
  ghostchrome waitfor --url-contains "/dashboard" --timeout 15
  ghostchrome waitfor --title-contains "Logged in"
  ghostchrome waitfor --count ".athing" --min 30 --timeout 5`,
	Args: cobra.RangeArgs(0, 2),
	Run: func(cmd *cobra.Command, args []string) {
		// Backward-compat: first positional = selector if --selector is not set.
		selector := flagWaitforSelector
		targetURL := ""
		if len(args) > 0 {
			if selector == "" {
				selector = args[0]
			} else {
				targetURL = args[0]
			}
		}
		if len(args) > 1 {
			targetURL = args[1]
		}

		if selector == "" && flagWaitforURLContains == "" && flagWaitforTitleContain == "" && flagWaitforCountSel == "" {
			exitErr("waitfor", fmt.Errorf("need one of: --selector, --url-contains, --title-contains, --count"))
		}

		b, page := openPage()
		defer b.Close()

		navigateIfRequested(page, targetURL, "load")

		timeout := time.Duration(flagWaitforTimeout) * time.Second
		cond, err := waitForAnyCondition(page, timeout, selector, flagWaitforURLContains, flagWaitforTitleContain, flagWaitforCountSel)
		if err != nil {
			exitErr("waitfor", err)
		}

		type waitforResult struct {
			Action    string `json:"action"`
			Matched   string `json:"matched"`
			Selector  string `json:"selector,omitempty"`
			URLLike   string `json:"url_contains,omitempty"`
			TitleLike string `json:"title_contains,omitempty"`
			Count     int    `json:"count,omitempty"`
			TimeMs    int64  `json:"time_ms"`
		}

		text := fmt.Sprintf("[waitfor] matched %s in %dms", cond.matched, cond.tookMs)
		output(&waitforResult{
			Action:    "waitfor",
			Matched:   cond.matched,
			Selector:  selector,
			URLLike:   flagWaitforURLContains,
			TitleLike: flagWaitforTitleContain,
			Count:     cond.count,
			TimeMs:    cond.tookMs,
		}, text)
	},
}

type waitResult struct {
	matched string
	count   int
	tookMs  int64
}

// waitForAnyCondition polls the page at 200 ms intervals until any of the
// provided conditions matches (selector, URL substring, title substring, count
// range). Returns the matched label or a timeout error.
func waitForAnyCondition(page *rod.Page, timeout time.Duration, selector, urlContains, titleContains, countSel string) (*waitResult, error) {
	start := time.Now()
	deadline := start.Add(timeout)

	for {
		if selector != "" {
			if ok := engine.HasSelector(page, selector); ok {
				return &waitResult{matched: "selector", tookMs: since(start)}, nil
			}
		}
		if urlContains != "" {
			info, _ := page.Info()
			if info != nil && strings.Contains(info.URL, urlContains) {
				return &waitResult{matched: "url_contains", tookMs: since(start)}, nil
			}
		}
		if titleContains != "" {
			info, _ := page.Info()
			if info != nil && strings.Contains(info.Title, titleContains) {
				return &waitResult{matched: "title_contains", tookMs: since(start)}, nil
			}
		}
		if countSel != "" {
			n := engine.CountSelector(page, countSel)
			switch {
			case flagWaitforCountMin > 0 && n >= flagWaitforCountMin && (flagWaitforCountMax == 0 || n <= flagWaitforCountMax):
				return &waitResult{matched: "count", count: n, tookMs: since(start)}, nil
			case flagWaitforCountMin == 0 && flagWaitforCountMax > 0 && n <= flagWaitforCountMax:
				return &waitResult{matched: "count", count: n, tookMs: since(start)}, nil
			}
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout after %s (matched nothing)", timeout)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func since(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

func init() {
	waitforCmd.Flags().IntVar(&flagWaitforTimeout, "timeout", 10, "Timeout in seconds")
	waitforCmd.Flags().StringVar(&flagWaitforSelector, "selector", "", "Wait until a CSS selector appears")
	waitforCmd.Flags().StringVar(&flagWaitforURLContains, "url-contains", "", "Wait until document.URL contains substring")
	waitforCmd.Flags().StringVar(&flagWaitforTitleContain, "title-contains", "", "Wait until document.title contains substring")
	waitforCmd.Flags().StringVar(&flagWaitforCountSel, "count", "", "Wait until the count of matching elements satisfies --min/--max")
	waitforCmd.Flags().IntVar(&flagWaitforCountMin, "min", 0, "Minimum count (used with --count)")
	waitforCmd.Flags().IntVar(&flagWaitforCountMax, "max", 0, "Maximum count (used with --count)")
	rootCmd.AddCommand(waitforCmd)
}
