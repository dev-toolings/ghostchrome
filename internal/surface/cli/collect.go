package cli

import (
	"errors"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/inspect"
	"github.com/spf13/cobra"
)

var (
	flagCollectLimit    int
	flagCollectParallel int
)

var collectCmd = &cobra.Command{
	Use:   "collect <url> [url2] [url3...]",
	Short: "Auto-detect and extract listings from one or more pages",
	Long: `Collect automatically detects repeated listing items on a page
(product cards, search results, classified ads) and extracts structured data.

It finds price patterns (€, $, £), identifies card boundaries,
and extracts title, price, URL, and metadata (year, km, location, fuel, etc.).

Multiple URLs are scraped in parallel using separate browser tabs.
No CSS selectors needed — it works dynamically on any listing page.

Examples:
  ghostchrome collect https://paruvendu.fr/a/voiture-occasion/peugeot/508/ --stealth
  ghostchrome collect url1 url2 url3 --stealth --connect ws://...
  ghostchrome collect https://www.amazon.fr/s?k=laptop --format json --limit 10`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 1 {
			// Single URL: simple path
			runSingleCollect(args[0])
		} else {
			// Multi URL: parallel tabs
			runMultiCollect(args)
		}
	},
}

func runSingleCollect(targetURL string) {
	b, page := openPage()
	defer b.Close()

	navigateIfRequested(page, targetURL, "stable")

	result, err := inspect.Collect(page, flagCollectLimit)
	if err != nil {
		exitErr("collect", err)
	}

	text := inspect.FormatCollect(result)
	output(result, text)
}

func runMultiCollect(urls []string) {
	b, err := engine.NewBrowserWith(buildBrowserOpts())
	if err != nil {
		exitErr("browser", err)
	}
	defer b.Close()

	// Get the underlying rod.Browser for multi-tab
	rodBrowser := b.RodBrowser()
	if rodBrowser == nil {
		exitErr("collect", errors.New("no browser available"))
	}

	result := inspect.MultiCollect(rodBrowser, urls, flagCollectLimit, flagStealth, flagCollectParallel)

	text := inspect.FormatMultiCollect(result)
	output(result, text)
}

func init() {
	collectCmd.Flags().IntVar(&flagCollectLimit, "limit", 50, "Maximum number of items to collect per site")
	collectCmd.Flags().IntVar(&flagCollectParallel, "parallel", 5, "Maximum number of tabs opened concurrently in multi-URL mode")
	rootCmd.AddCommand(collectCmd)
}
