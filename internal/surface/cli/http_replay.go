package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/sites"
	"github.com/spf13/cobra"
)

var (
	flagReplayMethod       string
	flagReplayHeaders      []string
	flagReplayBody         string
	flagReplayTimeoutMs    int
	flagReplayMaxBodyBytes int
	flagReplayPretty       bool
)

var httpReplayCmd = &cobra.Command{
	Use:   "http-replay <url>",
	Short: "Re-issue an HTTP request captured by sniff-api — pure HTTP, no Chrome",
	Long: `http-replay re-issues an HTTP request with optional header and body
overrides. Pure HTTP — no browser, no anti-bot detection. Returns
status, body, item_count, item_path, and sample_item.

Pair with sniff-api to scrape any SPA marketplace at API speed:
  1. ghostchrome sniff-api https://site.com/search  # discover the API
  2. ghostchrome http-replay https://api.site.com/search \
       --method POST --header 'Content-Type=application/json' \
       --body '{"query":"audi","page":2}'

Examples:
  ghostchrome http-replay https://api.example.com/items
  ghostchrome http-replay https://api.example.com/search \
    --method POST \
    --header 'Authorization=Bearer TOKEN' \
    --body '{"q":"renault"}' \
    --format json`,
	Args: cobra.ExactArgs(1),
	Run:  runHTTPReplay,
}

func init() {
	httpReplayCmd.Flags().StringVar(&flagReplayMethod, "method", "GET", "HTTP method (GET, POST, PUT, ...)")
	httpReplayCmd.Flags().StringArrayVar(&flagReplayHeaders, "header", nil, "Header override K=V (repeatable)")
	httpReplayCmd.Flags().StringVar(&flagReplayBody, "body", "", "Raw request body (JSON or url-encoded). Empty for GET.")
	httpReplayCmd.Flags().IntVar(&flagReplayTimeoutMs, "timeout-ms", 15000, "Request timeout in milliseconds")
	httpReplayCmd.Flags().IntVar(&flagReplayMaxBodyBytes, "max-body-bytes", 262144, "Response body cap in bytes")
	httpReplayCmd.Flags().BoolVar(&flagReplayPretty, "pretty", false, "Pretty-print JSON output")
	rootCmd.AddCommand(httpReplayCmd)
}

func runHTTPReplay(_ *cobra.Command, args []string) {
	target := args[0]

	headers, err := parseReplayHeaders(flagReplayHeaders)
	if err != nil {
		exitErr("http-replay", err)
	}

	in := sites.ReplayInput{
		Method:       flagReplayMethod,
		URL:          target,
		Headers:      headers,
		Body:         flagReplayBody,
		TimeoutMs:    flagReplayTimeoutMs,
		MaxBodyBytes: flagReplayMaxBodyBytes,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(flagReplayTimeoutMs+2000)*time.Millisecond)
	defer cancel()

	res, err := sites.Replay(ctx, in)
	if err != nil {
		exitErr("http-replay", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if flagReplayPretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(res); err != nil {
		exitErr("http-replay encode", err)
	}
	fmt.Fprintf(os.Stderr, "[http-replay] %s status=%d bytes=%d items=%d elapsed=%dms\n",
		res.URL, res.Status, res.BodyBytes, res.ItemCount, res.DurationMs)
}

func parseReplayHeaders(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		idx := strings.IndexByte(p, '=')
		if idx <= 0 {
			return nil, fmt.Errorf("header %q must be in K=V format", p)
		}
		out[p[:idx]] = p[idx+1:]
	}
	return out, nil
}
