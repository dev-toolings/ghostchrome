// Package sites provides generic, site-agnostic discovery and replay
// helpers. The goal: identify a marketplace's hidden search backend
// (Algolia, Elastic, GraphQL, REST, ...) by sniffing real XHR traffic
// during a normal browse, then let the agent replay it directly via
// HTTP — no browser, no anti-bot detection, no fingerprint.
package sites

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// CapturedRequest is one XHR/fetch round-trip recorded during a sniff.
// All fields are JSON-friendly so the agent can transform and reuse them.
type CapturedRequest struct {
	Method        string            `json:"method"`
	URL           string            `json:"url"`
	Host          string            `json:"host"`
	Path          string            `json:"path"`
	Status        int               `json:"status"`
	MimeType      string            `json:"mime_type,omitempty"`
	RequestBody   string            `json:"request_body,omitempty"`
	ResponseBody  string            `json:"response_body,omitempty"`
	ResponseBytes int               `json:"response_bytes"`
	DurationMs    int64             `json:"duration_ms"`
	Headers       map[string]string `json:"headers,omitempty"`
	// Score is computed by the heuristic (higher = more likely a search API).
	Score int `json:"score"`
	// ItemCount is the size of the longest top-level array in the response,
	// when JSON. Heuristic for "this is a list of results".
	ItemCount int `json:"item_count,omitempty"`
	// ItemPath is the dotted JSON path to that array (e.g. "hits", "data.ads").
	ItemPath string `json:"item_path,omitempty"`
	// SampleItem is the first item of the array, truncated to ~400 chars.
	SampleItem string `json:"sample_item,omitempty"`
}

// pendingNet tracks one inflight request between requestWillBeSent and
// loadingFinished, so we can correlate request body + response body.
type pendingNet struct {
	method  string
	url     string
	body    string
	headers map[string]string
	started float64
	// hasPostData is set when Chrome flagged HasPostData=true but the body
	// itself was omitted from the requestWillBeSent event (Chrome elides
	// large POST bodies). We then fetch it lazily via Network.getRequestPostData.
	hasPostData bool
}

// SniffOptions controls how aggressively we sniff.
type SniffOptions struct {
	// Duration to keep the listener attached after navigate. Default 5s.
	Duration time.Duration
	// MaxBodyBytes: cap on response body bytes captured per request (avoids
	// huge HTML/JS in memory). Default 32 KiB. Bodies above the cap still
	// get parsed for top-level array detection — only the SampleItem is
	// truncated.
	MaxBodyBytes int
	// IncludeNonJSON: when true, also keep non-JSON candidates. Default false.
	IncludeNonJSON bool
}

// Sniff drives the page through the lifecycle (navigate → wait → drain),
// captures every request whose response is JSON-shaped, scores each one,
// and returns them ranked by score (highest first). The page is unchanged
// at exit; caller decides whether to close the session.
//
// Caller MUST have the page already navigated, OR pass an empty navigateTo
// and use a pre-warmed session. Most agents pass `navigateTo` with the
// listing-page URL.
func Sniff(ctx context.Context, page *rod.Page, navigateTo string, opts SniffOptions) ([]CapturedRequest, error) {
	if opts.Duration <= 0 {
		opts.Duration = 5 * time.Second
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 32 * 1024
	}

	// Enable Network domain (idempotent).
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return nil, fmt.Errorf("network enable: %w", err)
	}

	var (
		mu       sync.Mutex
		inflight = map[proto.NetworkRequestID]*pendingNet{}
		captured []CapturedRequest
	)

	// Standard rod pattern (see Page.WaitRequestIdle): WithCancel gives us
	// a context to stop the dispatcher; EachEvent's returned func is the
	// wait function that runs the dispatcher loop until the context is
	// cancelled. We must call wait() (in main goroutine) for events to
	// actually fire — see go-rod/rod#page.go EachEvent.
	scoped, cancel := page.WithCancel()
	wait := scoped.EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			// Skip stylesheet/script/image/font etc. early — only XHR/fetch
			// or Document POSTs (some GraphQL backends use Document type).
			t := strings.ToLower(string(e.Type))
			if t == "stylesheet" || t == "image" || t == "font" || t == "media" ||
				t == "script" || t == "websocket" || t == "manifest" {
				return
			}
			h := map[string]string{}
			for k, v := range e.Request.Headers {
				if s, ok := v.Val().(string); ok {
					h[k] = s
				}
			}
			mu.Lock()
			inflight[e.RequestID] = &pendingNet{
				method:      e.Request.Method,
				url:         e.Request.URL,
				body:        e.Request.PostData,
				headers:     h,
				started:     float64(e.Timestamp),
				hasPostData: e.Request.HasPostData,
			}
			mu.Unlock()
		},
		func(e *proto.NetworkResponseReceived) {
			mu.Lock()
			p, ok := inflight[e.RequestID]
			mu.Unlock()
			if !ok {
				return
			}
			// Stash mime + status into the pending so loadingFinished sees it.
			mu.Lock()
			if p.headers == nil {
				p.headers = map[string]string{}
			}
			p.headers["__mime"] = e.Response.MIMEType
			p.headers["__status"] = fmt.Sprintf("%d", e.Response.Status)
			mu.Unlock()
		},
		func(e *proto.NetworkLoadingFinished) {
			mu.Lock()
			p, ok := inflight[e.RequestID]
			if !ok {
				mu.Unlock()
				return
			}
			delete(inflight, e.RequestID)
			mu.Unlock()

			mime := p.headers["__mime"]
			isJSON := strings.Contains(mime, "json") ||
				strings.Contains(mime, "graphql") ||
				strings.Contains(mime, "javascript") // some APIs lie with text/javascript
			if !isJSON && !opts.IncludeNonJSON {
				return
			}

			// Fetch the response body via CDP.
			body := ""
			if r, err := (proto.NetworkGetResponseBody{RequestID: e.RequestID}).Call(page); err == nil && r != nil {
				if r.Base64Encoded {
					if dec, err := base64.StdEncoding.DecodeString(r.Body); err == nil {
						body = string(dec)
					}
				} else {
					body = r.Body
				}
			}

			// CDP elides large POST bodies from requestWillBeSent ("postData
			// might still be omitted when [HasPostData] is true when the data
			// is too long" — DevTools protocol docs). For Algolia/multi-query
			// payloads ~1-3 KiB this triggers; we fetch the body lazily here.
			if p.body == "" && p.hasPostData {
				if r, err := (proto.NetworkGetRequestPostData{RequestID: e.RequestID}).Call(page); err == nil && r != nil {
					p.body = r.PostData
				}
			}

			cap_ := buildCaptured(p, body, e, opts.MaxBodyBytes)
			cap_.Status = parseIntOr(p.headers["__status"], 0)
			cap_.MimeType = mime
			delete(cap_.Headers, "__mime")
			delete(cap_.Headers, "__status")

			mu.Lock()
			captured = append(captured, cap_)
			mu.Unlock()
		},
		func(e *proto.NetworkLoadingFailed) {
			mu.Lock()
			delete(inflight, e.RequestID)
			mu.Unlock()
		},
	)

	// Drive the navigation BEFORE running wait() so the listeners see the
	// initial document + every XHR fired during load. EachEvent's dispatcher
	// runs in its own goroutine via wait(), so navigate's CDP calls happen
	// concurrently and the listener picks them up.
	if navigateTo != "" {
		if err := page.Navigate(navigateTo); err != nil {
			cancel()
			return nil, fmt.Errorf("navigate: %w", err)
		}
		_ = page.WaitLoad()
	}

	// Schedule the cancel after the drain window. This unblocks wait()
	// so we can read the captured slice and return.
	go func() {
		t := time.NewTimer(opts.Duration)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
		}
		cancel()
	}()

	// Block here, dispatching events to our callbacks until cancel fires.
	wait()

	mu.Lock()
	out := make([]CapturedRequest, len(captured))
	copy(out, captured)
	mu.Unlock()

	rankCandidates(out)
	return out, nil
}

// buildCaptured assembles the CapturedRequest from the inflight pending
// + final loading event. Body parsing happens here.
func buildCaptured(p *pendingNet, body string, e *proto.NetworkLoadingFinished, maxBodyBytes int) CapturedRequest {
	out := CapturedRequest{
		Method:        p.method,
		URL:           p.url,
		RequestBody:   p.body,
		Headers:       p.headers,
		ResponseBytes: len(body),
	}
	if u, err := url.Parse(p.url); err == nil {
		out.Host = u.Host
		out.Path = u.Path
	}
	if p.started > 0 {
		out.DurationMs = int64((float64(e.Timestamp) - p.started) * 1000)
	}

	// Try to detect a top-level array in the JSON body.
	if path, count, sample := findTopLevelArray(body, maxBodyBytes); count > 0 {
		out.ItemCount = count
		out.ItemPath = path
		out.SampleItem = sample
	}

	// Truncate huge bodies for transport. We keep at least 4 KiB so the
	// agent can see the structure even when the array detection failed.
	if len(out.ResponseBody) == 0 && len(body) > 0 {
		if len(body) > 4096 {
			out.ResponseBody = body[:4096] + "…"
		} else {
			out.ResponseBody = body
		}
	}
	return out
}

// findTopLevelArray scans a JSON document and returns the path + length
// of the largest top-level (or one-level-nested) array, plus a truncated
// preview of its first item. Heuristic for "this is a list of results".
func findTopLevelArray(body string, maxSampleBytes int) (path string, count int, sample string) {
	if len(body) == 0 {
		return "", 0, ""
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return "", 0, ""
	}
	bestPath, bestCount, bestSample := "", 0, ""
	var walk func(node any, p string, depth int)
	walk = func(node any, p string, depth int) {
		switch n := node.(type) {
		case []any:
			if len(n) > bestCount {
				bestCount = len(n)
				bestPath = p
				if len(n) > 0 {
					if raw, err := json.Marshal(n[0]); err == nil {
						s := string(raw)
						if len(s) > maxSampleBytes/8 {
							s = s[:maxSampleBytes/8] + "…"
						}
						bestSample = s
					}
				}
			}
		case map[string]any:
			if depth > 3 {
				return
			}
			for k, child := range n {
				next := k
				if p != "" {
					next = p + "." + k
				}
				walk(child, next, depth+1)
			}
		}
	}
	walk(v, "", 0)
	return bestPath, bestCount, bestSample
}

// rankCandidates sorts in-place by Score descending. Score = ItemCount * 10
// + small bonuses for POST + JSON mime.
func rankCandidates(items []CapturedRequest) {
	for i := range items {
		s := items[i].ItemCount * 10
		if items[i].Method == "POST" {
			s += 5
		}
		if strings.Contains(items[i].MimeType, "json") {
			s += 3
		}
		// Penalize asset-like URLs.
		if strings.Contains(items[i].Path, "/sw.js") ||
			strings.Contains(items[i].Path, "/manifest") ||
			strings.HasSuffix(items[i].Path, ".png") ||
			strings.HasSuffix(items[i].Path, ".svg") {
			s -= 100
		}
		items[i].Score = s
	}
	// Simple bubble — small N (~10-50 candidates).
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].Score > items[i].Score {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func parseIntOr(s string, def int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
		return n
	}
	return def
}
