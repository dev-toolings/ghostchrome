package engine

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// FastFetchOpts tunes the no-browser HTTP path.
type FastFetchOpts struct {
	FallbackTLS    bool          // retry a blocked/SSR-empty GET with a Chrome TLS profile before a browser
	UserAgent      string        // "" → derived from runtime.GOOS like ApplyDefaultPageProfile
	AcceptLanguage string        // default "fr-FR,fr;q=0.9,en-US;q=0.8,en;q=0.7"
	Timeout        time.Duration // default 8s
	ExtraHeaders   map[string]string
	Proxy          string // optional http(s):// or socks5:// proxy URL
}

// FastResult is what FastFetch returns. Blocked means "this page is gated by
// an anti-bot challenge — do NOT trust the body, fall back to a real browser".
//
// NextData is the raw __NEXT_DATA__ JSON when present; SSRPayloads holds the
// full set of structured data islands found in the body (Next, Nuxt, Apollo,
// Initial-State, JSON-LD…). Modern recipes pick the first match they
// understand; legacy callers can keep using NextData.
type FastResult struct {
	Transport   string // "http" or "http-tls"
	Status      int
	URL         string // final URL after redirects
	HTML        string
	NextData    []byte // raw JSON from <script id="__NEXT_DATA__">; nil if absent
	SSRPayloads []SSRPayload
	Blocked     bool   // anti-bot challenge detected
	Reason      string // human-readable explanation when Blocked or no payload extracted
	Elapsed     time.Duration
}

// FastFetch performs a single GET with realistic browser headers and inspects
// the response for SSR data and anti-bot markers. It's a best-effort fast
// path: callers must check (Blocked || NextData == nil) and decide whether
// to fall back to a Chrome-driven recipe.
func FastFetch(ctx context.Context, url string, opts FastFetchOpts) (*FastResult, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	result, err := fastFetch(ctx, url, opts, false)
	if !opts.FallbackTLS || ctx.Err() != nil || (err == nil && !needsTLSFallback(result)) {
		return result, err
	}
	// Only HTTP(S) GET requests enter this fallback. No browser cookies are imported.
	retry, retryErr := fastFetch(ctx, url, opts, true)
	if retryErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, fmt.Errorf("fastfetch: HTTP and TLS attempts failed")
		}
		result.Reason += "; TLS fallback failed"
		return result, nil
	}
	retry.Elapsed = time.Since(started)
	return retry, nil
}

func needsTLSFallback(result *FastResult) bool {
	if result == nil || result.Status == 429 || result.Status == 401 {
		return false
	}
	return result.Blocked || (result.Status >= 200 && result.Status < 300 && len(result.SSRPayloads) == 0)
}

func fastFetch(ctx context.Context, url string, opts FastFetchOpts, browserTLS bool) (*FastResult, error) {
	if url == "" {
		return nil, errors.New("fastfetch: empty url")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = fastFetchUA()
	}
	al := opts.AcceptLanguage
	if al == "" {
		al = defaultAcceptLanguage
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("fastfetch: build request: %w", err)
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", al)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	for k, v := range opts.ExtraHeaders {
		req.Header.Set(k, v)
	}

	client, err := BuildHTTPClient(HTTPClientOpts{Proxy: opts.Proxy, Timeout: timeout})
	if err != nil {
		return nil, fmt.Errorf("fastfetch: %w", err)
	}

	start := time.Now()
	var resp *http.Response
	if browserTLS {
		resp, err = doChromeTLS(req, opts)
	} else {
		defer client.CloseIdleConnections()
		resp, err = client.Do(req)
	}
	if err != nil {
		return nil, fmt.Errorf("fastfetch: do: %w", err)
	}
	defer resp.Body.Close()

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("fastfetch: read body: %w", err)
	}
	elapsed := time.Since(start)

	html := string(body)
	res := &FastResult{
		Transport: "http",
		Status:    resp.StatusCode,
		URL:       resp.Request.URL.String(),
		HTML:      html,
		Elapsed:   elapsed,
	}

	if browserTLS {
		res.Transport = "http-tls"
	}
	if blocked, reason := detectAntiBot(resp.StatusCode, html); blocked {
		res.Blocked = true
		res.Reason = reason
		return res, nil
	}

	res.SSRPayloads = ExtractSSRPayloads(html)
	for _, p := range res.SSRPayloads {
		if p.Source == SourceNextData {
			res.NextData = p.Data
			break
		}
	}
	if len(res.SSRPayloads) == 0 {
		res.Reason = "no SSR payload (Next/Nuxt/Apollo/Initial-State/JSON-LD) found in response"
	}
	return res, nil
}

// maxResponseBodyBytes caps body reads to bound memory under hostile or
// oversized responses. 100 MiB is well above realistic HTML/JSON payloads.
const maxResponseBodyBytes = 100 << 20

// readResponseBody handles gzip transparently and enforces maxResponseBodyBytes.
// Net/http auto-decompresses only when Accept-Encoding wasn't manually set;
// we set it, so we own the dance.
func readResponseBody(resp *http.Response) ([]byte, error) {
	var reader io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		reader = gr
	}
	limited := io.LimitReader(reader, maxResponseBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxResponseBodyBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxResponseBodyBytes)
	}
	return body, nil
}

// ExtractNextData scans for the canonical Next.js SSR data island in arbitrary
// HTML and returns the raw JSON payload. Returns nil when the marker is
// absent. Exported so callers that obtain HTML through a different path
// (e.g. Chrome page.HTML()) can reuse the same parser.
func ExtractNextData(html string) []byte { return extractNextData(html) }

// extractNextData scans for the canonical Next.js SSR data island. We do a
// fast string search rather than HTML parsing — the marker is unique and
// the script body never contains a literal "</script>" (Next.js escapes it).
func extractNextData(html string) []byte {
	const marker = `<script id="__NEXT_DATA__" type="application/json">`
	i := strings.Index(html, marker)
	if i < 0 {
		// Some Next.js builds drop the type attribute or order it differently.
		alt := `id="__NEXT_DATA__"`
		j := strings.Index(html, alt)
		if j < 0 {
			return nil
		}
		gt := strings.IndexByte(html[j:], '>')
		if gt < 0 {
			return nil
		}
		i = j + gt + 1
	} else {
		i += len(marker)
	}
	end := strings.Index(html[i:], "</script>")
	if end < 0 {
		return nil
	}
	return []byte(strings.TrimSpace(html[i : i+end]))
}

// detectAntiBot returns (blocked, reason). False positives are costly (we'd
// pay the Chrome fallback for a page that was actually fine), so we are
// conservative and only flag what's almost certainly a challenge.
func detectAntiBot(status int, html string) (bool, string) {
	switch status {
	case 0:
		return true, "no response"
	case 403:
		return true, "HTTP 403"
	case 429:
		return true, "HTTP 429 (rate limited)"
	case 503:
		return true, "HTTP 503 (likely Cloudflare challenge)"
	}
	lower := strings.ToLower(html)
	// DataDome interstitial markers
	if strings.Contains(lower, "geo.captcha-delivery.com") || strings.Contains(lower, "dd_cookie_test") {
		// The DataDome SDK (`/tags.js`) is loaded on virtually every
		// autoscout24 / leboncoin page, even successful ones. Only flag
		// when the page is clearly the challenge interstitial — i.e.
		// the body is short and has no real content frame.
		if len(html) < 30_000 && (strings.Contains(lower, "you have been blocked") ||
			strings.Contains(lower, "interstitial") ||
			strings.Contains(lower, "<title>access denied")) {
			return true, "DataDome challenge interstitial"
		}
	}
	// Cloudflare "Just a moment..."
	if strings.Contains(lower, "cf-browser-verification") ||
		strings.Contains(lower, "cf_chl_opt") ||
		strings.Contains(lower, "challenge-platform") && strings.Contains(lower, "<title>just a moment") {
		return true, "Cloudflare browser verification"
	}
	// hCaptcha / reCAPTCHA full pages
	if strings.Contains(lower, "<title>captcha") {
		return true, "captcha page"
	}
	return false, ""
}

func fastFetchUA() string {
	switch runtime.GOOS {
	case "darwin":
		return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + chromeMajor + " Safari/537.36"
	case "windows":
		return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + chromeMajor + " Safari/537.36"
	default:
		return "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + chromeMajor + " Safari/537.36"
	}
}
