package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/spf13/cobra"
)

var (
	fastfetchUA              string
	fastfetchAcceptLanguage  string
	fastfetchHeaders         []string
	fastfetchTimeoutMs       int
	fastfetchOutput          string
	fastfetchRaw             bool
	fastfetchNextDataOnly    bool
	fastfetchPretty          bool
	fastfetchFallbackTLS     bool
	fastfetchFallbackBrowser bool
	fastfetchIncludePayloads bool
)

var fastfetchCmd = &cobra.Command{
	Use:   "fastfetch <url>",
	Short: "Fetch a URL via plain HTTP, detect anti-bot challenges, and extract __NEXT_DATA__ (no Chrome by default)",
	Long: `fastfetch performs a single HTTP GET with realistic browser headers and
returns a JSON envelope describing the response: status, anti-bot verdict,
elapsed time, raw HTML size, and the parsed __NEXT_DATA__ payload when the
target page is a Next.js app.

It is the no-browser fast path that ghostchrome falls back to before
spawning Chrome. Useful for:
  - SSR-rendered sites where DOM parsing is overkill
  - Quickly probing whether a target is gated by DataDome / Cloudflare
  - Bulk scraping pipelines where 95% of pages render server-side

Set --fallback-tls to try a Chrome-compatible TLS/HTTP2 transport first,
without Python or a browser. HTTP 429 and 401 are not retried by this fallback.
When a target is blocked, set --fallback-browser to spawn Chrome and
return the post-render HTML (and __NEXT_DATA__ if present).

Examples:
  ghostchrome fastfetch https://www.autoscout24.fr/lst/audi/a3
  ghostchrome fastfetch --next-data https://www.leboncoin.fr/voitures/...
  ghostchrome fastfetch --raw -o page.html https://example.com
  ghostchrome fastfetch --header 'Cookie=foo=bar' --header 'Referer=https://google.com' https://target.com
  ghostchrome fastfetch --fallback-browser --stealth https://gated.fr`,
	Args: cobra.ExactArgs(1),
	Run:  runFastfetch,
}

func init() {
	fastfetchCmd.Flags().StringVar(&fastfetchUA, "ua", "", "User-Agent override (default: realistic Chrome UA matching runtime.GOOS)")
	fastfetchCmd.Flags().StringVar(&fastfetchAcceptLanguage, "accept-language", "", "Accept-Language header (default: fr-FR,fr;q=0.9,en-US;q=0.8,en;q=0.7)")
	fastfetchCmd.Flags().StringArrayVar(&fastfetchHeaders, "header", nil, "Extra request header K=V (repeatable)")
	fastfetchCmd.Flags().IntVar(&fastfetchTimeoutMs, "timeout-ms", 8000, "HTTP timeout in milliseconds")
	fastfetchCmd.Flags().StringVarP(&fastfetchOutput, "output", "o", "", "Write payload to this path (default: stdout)")
	fastfetchCmd.Flags().BoolVar(&fastfetchRaw, "raw", false, "Output the raw HTML body instead of the JSON envelope")
	fastfetchCmd.Flags().BoolVar(&fastfetchNextDataOnly, "next-data", false, "Output only the parsed __NEXT_DATA__ JSON (exit 1 if absent)")
	fastfetchCmd.Flags().BoolVar(&fastfetchPretty, "pretty", false, "Pretty-print JSON output")
	fastfetchCmd.Flags().BoolVar(&fastfetchFallbackBrowser, "fallback-browser", false, "On Blocked or no SSR payload, spawn Chrome and retry (uses --stealth, --user-profile, etc.)")
	fastfetchCmd.Flags().BoolVar(&fastfetchIncludePayloads, "include-payloads", false, "Embed every SSR payload (Next/Nuxt/Apollo/JSON-LD) in the envelope — can be MB-sized")
	fastfetchCmd.Flags().BoolVar(&fastfetchFallbackTLS, "fallback-tls", false, "Retry blocked or SSR-empty GETs with Chrome TLS before the optional browser fallback")
	rootCmd.AddCommand(fastfetchCmd)
}

// fastfetchEnvelope is the default JSON output shape — ergonomic for jq pipes.
type fastfetchEnvelope struct {
	URL         string              `json:"url"`
	Status      int                 `json:"status"`
	ElapsedMs   int64               `json:"elapsed_ms"`
	Mode        string              `json:"mode"` // "http" or "browser"
	Blocked     bool                `json:"blocked,omitempty"`
	Reason      string              `json:"reason,omitempty"`
	HTMLSize    int                 `json:"html_size"`
	HasNextData bool                `json:"has_next_data"`
	NextData    json.RawMessage     `json:"next_data,omitempty"`
	SSRSources  []string            `json:"ssr_sources,omitempty"`
	SSRPayloads []engine.SSRPayload `json:"ssr_payloads,omitempty"`
	JSONLDTypes []string            `json:"json_ld_types,omitempty"`
}

func runFastfetch(_ *cobra.Command, args []string) {
	target := args[0]

	headers, err := parseFastfetchHeaders(fastfetchHeaders)
	if err != nil {
		exitErr("fastfetch", err)
	}

	opts := engine.FastFetchOpts{
		FallbackTLS:    fastfetchFallbackTLS,
		UserAgent:      fastfetchUA,
		AcceptLanguage: fastfetchAcceptLanguage,
		Timeout:        time.Duration(fastfetchTimeoutMs) * time.Millisecond,
		ExtraHeaders:   headers,
		Proxy:          flagProxy,
	}

	tStart := time.Now()
	res, err := engine.FastFetch(context.Background(), target, opts)
	if err != nil {
		exitErr("fastfetch", err)
	}
	mode := res.Transport

	// Optional Chrome fallback when the fast path can't deliver.
	if fastfetchFallbackBrowser && (res.Blocked || res.NextData == nil) {
		fmt.Fprintf(os.Stderr, "[fastfetch] http path failed (%s) — spawning Chrome\n", fastfetchFallbackReason(res))
		fb, err := fastfetchViaBrowser(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[fastfetch] browser fallback failed: %v\n", err)
		} else {
			res = fb
			mode = "browser"
		}
	}

	var payload bytes.Buffer
	w := &payload

	switch {
	case fastfetchRaw:
		if _, err := w.Write([]byte(res.HTML)); err != nil {
			exitErr("fastfetch write", err)
		}
	case fastfetchNextDataOnly:
		if res.NextData == nil {
			fmt.Fprintf(os.Stderr, "[fastfetch] no __NEXT_DATA__ (status=%d blocked=%v reason=%q)\n",
				res.Status, res.Blocked, res.Reason)
			os.Exit(1)
		}
		if fastfetchPretty {
			var v interface{}
			if err := json.Unmarshal(res.NextData, &v); err != nil {
				exitErr("fastfetch next-data unmarshal", err)
			}
			enc := json.NewEncoder(w)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			if err := enc.Encode(v); err != nil {
				exitErr("fastfetch next-data encode", err)
			}
		} else {
			if _, err := w.Write(res.NextData); err != nil {
				exitErr("fastfetch next-data write", err)
			}
			fmt.Fprintln(w)
		}
	default:
		sources := make([]string, 0, len(res.SSRPayloads))
		var ldTypes []string
		for _, p := range res.SSRPayloads {
			sources = append(sources, string(p.Source))
			if p.Source == engine.SourceJSONLD && p.Type != "" {
				ldTypes = append(ldTypes, p.Type)
			}
		}
		env := fastfetchEnvelope{
			URL:         res.URL,
			Status:      res.Status,
			ElapsedMs:   time.Since(tStart).Milliseconds(),
			Mode:        mode,
			Blocked:     res.Blocked,
			Reason:      res.Reason,
			HTMLSize:    len(res.HTML),
			HasNextData: res.NextData != nil,
			NextData:    res.NextData,
			SSRSources:  sources,
			JSONLDTypes: ldTypes,
		}
		if fastfetchIncludePayloads {
			env.SSRPayloads = res.SSRPayloads
		}
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		if fastfetchPretty {
			enc.SetIndent("", "  ")
		}
		if err := enc.Encode(env); err != nil {
			exitErr("fastfetch encode", err)
		}
	}
	if err := writeFastfetchOutput(payload.Bytes()); err != nil {
		exitErr("fastfetch write", err)
	}

	// Always print a one-line stderr summary when not raw/next-data so
	// pipelines can grep for status without parsing JSON.
	if !fastfetchRaw && !fastfetchNextDataOnly {
		fmt.Fprintf(os.Stderr, "[fastfetch] %s mode=%s status=%d blocked=%v has_next_data=%v elapsed=%dms\n",
			res.URL, mode, res.Status, res.Blocked, res.NextData != nil, time.Since(tStart).Milliseconds())
	}
}

func parseFastfetchHeaders(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for _, h := range raw {
		idx := strings.IndexAny(h, "=:")
		if idx <= 0 {
			return nil, fmt.Errorf("invalid --header %q: expected K=V or K:V", h)
		}
		k := strings.TrimSpace(h[:idx])
		v := strings.TrimSpace(h[idx+1:])
		if k == "" {
			return nil, fmt.Errorf("invalid --header %q: empty key", h)
		}
		out[k] = v
	}
	return out, nil
}

func fastfetchFallbackReason(res *engine.FastResult) string {
	if res.Blocked {
		return "blocked: " + res.Reason
	}
	if res.NextData == nil {
		return "no __NEXT_DATA__"
	}
	return "ok"
}

// fastfetchViaBrowser spawns Chrome (respecting global flags like --stealth /
// --user-profile / --connect), navigates to the target, and reuses the same
// FastResult shape so callers stay agnostic to which path produced the body.
func fastfetchViaBrowser(target string) (*engine.FastResult, error) {
	opts := buildBrowserOpts()
	b, err := engine.NewBrowserWith(opts)
	if err != nil {
		return nil, fmt.Errorf("browser: %w", err)
	}
	defer b.Close()

	page, err := b.Page()
	if err != nil {
		return nil, fmt.Errorf("page: %w", err)
	}
	applyStealthIfNeeded(page)

	tStart := time.Now()
	info, err := engine.Navigate(page, target, "load")
	if err != nil {
		return nil, fmt.Errorf("navigate: %w", err)
	}
	dismissCookiesIfNeeded(page)

	html, err := page.HTML()
	if err != nil {
		return nil, fmt.Errorf("page html: %w", err)
	}

	res := &engine.FastResult{
		Status:  info.Status,
		URL:     info.URL,
		HTML:    html,
		Elapsed: time.Since(tStart),
	}
	if data := engine.ExtractNextData(html); data != nil {
		res.NextData = data
	}
	return res, nil
}

// writeFastfetchOutput applies the same secret and output-size controls as the
// common command renderer. Fastfetch already holds its response in memory, so
// buffering here adds no unbounded allocation while closing the raw-HTML
// bypass around global output safety.
func writeFastfetchOutput(data []byte) error {
	data = redactOutput(data, flagOutputSecrets)
	if fastfetchOutput == "" {
		oldRaw := flagRaw
		flagRaw = flagRaw || fastfetchRaw || fastfetchNextDataOnly
		limited, err := limitOutput(data)
		flagRaw = oldRaw
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(limited)
		return err
	}
	safe, err := validateOutputPath(fastfetchOutput)
	if err != nil {
		return err
	}
	if err := os.WriteFile(safe, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(safe, 0o600)
}
