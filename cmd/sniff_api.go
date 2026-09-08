package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/sites"
	"github.com/spf13/cobra"
)

var (
	flagSniffDrainSeconds int
	flagSniffTopN         int
	flagSniffMaxBodyBytes int
	flagSniffPretty       bool
)

var sniffAPICmd = &cobra.Command{
	Use:   "sniff-api <url>",
	Short: "Sniff XHR/fetch traffic on a live page and rank JSON API candidates",
	Long: `sniff-api navigates to a URL, captures every JSON XHR/fetch the page
makes during a configurable drain window, scores them by likelihood of
being a search backend (item count + POST/JSON bonuses), and returns
the top candidates with method, url, headers, request_body,
response_body, item_count, and sample_item.

Use this BEFORE http-replay to discover an SPA's hidden API. Generic —
works on Algolia/Elastic/REST/GraphQL marketplaces.

Examples:
  ghostchrome sniff-api https://www.capcar.fr/voitures
  ghostchrome sniff-api https://www.leboncoin.fr/recherche?category=2 \
    --drain-seconds 8 --top-n 3 --format json`,
	Args: cobra.ExactArgs(1),
	Run:  runSniffAPI,
}

func init() {
	sniffAPICmd.Flags().IntVar(&flagSniffDrainSeconds, "drain-seconds", 5, "Seconds to keep listening after page load fires")
	sniffAPICmd.Flags().IntVar(&flagSniffTopN, "top-n", 5, "Return top N candidates by score (max 20)")
	sniffAPICmd.Flags().IntVar(&flagSniffMaxBodyBytes, "max-body-bytes", 32768, "Per-response body cap in bytes")
	sniffAPICmd.Flags().BoolVar(&flagSniffPretty, "pretty", false, "Pretty-print JSON output")
	rootCmd.AddCommand(sniffAPICmd)
}

func runSniffAPI(_ *cobra.Command, args []string) {
	target := args[0]

	topN := flagSniffTopN
	if topN > 20 {
		topN = 20
	}

	b, page := openPage()
	defer b.Close()

	applyStealthIfNeeded(page)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(flagTimeout)*time.Second+time.Duration(flagSniffDrainSeconds)*time.Second)
	defer cancel()

	captured, err := sites.Sniff(ctx, page, target, sites.SniffOptions{
		Duration:     time.Duration(flagSniffDrainSeconds) * time.Second,
		MaxBodyBytes: flagSniffMaxBodyBytes,
	})
	if err != nil {
		exitErr("sniff-api", err)
	}

	if topN > 0 && len(captured) > topN {
		captured = captured[:topN]
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if flagSniffPretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(captured); err != nil {
		exitErr("sniff-api encode", err)
	}
	fmt.Fprintf(os.Stderr, "[sniff-api] %d JSON candidates captured (top %d returned)\n", len(captured), topN)
}
