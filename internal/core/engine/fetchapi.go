package engine

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIRequest is the cross-method, JSON-aware request shape used by
// FastFetchAPI. Headers and Body are sent as-is; the helper only owns
// transport-level details (gzip handling, timeout, JSON detection).
type APIRequest struct {
	Method  string            // GET, POST, PUT, DELETE, … (default GET)
	URL     string            // absolute, with query string
	Headers map[string]string // any custom headers
	Body    []byte            // request body, plain bytes
	Timeout time.Duration     // per-request deadline (default 8s)
	Proxy   string            // optional http(s):// or socks5:// proxy URL
}

// APIResponse is what FastFetchAPI returns. Body is the decoded payload
// (gzip handled). JSON is non-nil when Body parses cleanly as JSON,
// regardless of the response Content-Type — many APIs lie about the type.
type APIResponse struct {
	Status        int             `json:"status"`
	URL           string          `json:"url"`
	Headers       http.Header     `json:"headers,omitempty"`
	Body          []byte          `json:"-"`
	BodyText      string          `json:"body_text,omitempty"`
	JSON          json.RawMessage `json:"json,omitempty"`
	IsJSON        bool            `json:"is_json"`
	Elapsed       time.Duration   `json:"-"`
	ElapsedMs     int64           `json:"elapsed_ms"`
	ContentLength int64           `json:"content_length"`
}

// FastFetchAPI is the JSON-API counterpart to FastFetch. Use it to replay
// XHR endpoints captured via `ghostchrome --observe` or to hit a known
// API directly (Algolia, vendor JSON APIs, public REST). It does not
// attempt anti-bot detection — APIs that need cookies/tokens fail with a
// clean status code that the caller acts on.
//
// Differences from FastFetch:
//   - generic method (GET/POST/PUT/…)
//   - no SSR / __NEXT_DATA__ scanning
//   - tries to JSON-parse the body opportunistically; falls back to text
func FastFetchAPI(ctx context.Context, req APIRequest) (*APIResponse, error) {
	if req.URL == "" {
		return nil, errors.New("fetchapi: empty url")
	}
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return nil, fmt.Errorf("fetchapi: build request: %w", err)
	}

	// Conservative defaults. Caller can override via Headers.
	httpReq.Header.Set("Accept", "application/json, text/plain, */*")
	httpReq.Header.Set("Accept-Encoding", "gzip")
	httpReq.Header.Set("User-Agent", fastFetchUA())
	if len(req.Body) > 0 && httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	client, err := BuildHTTPClient(HTTPClientOpts{Proxy: req.Proxy, Timeout: timeout})
	if err != nil {
		return nil, fmt.Errorf("fetchapi: %w", err)
	}
	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("fetchapi: do: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("fetchapi: read body: %w", err)
	}
	elapsed := time.Since(start)

	out := &APIResponse{
		Status:        resp.StatusCode,
		URL:           resp.Request.URL.String(),
		Headers:       resp.Header,
		Body:          bodyBytes,
		Elapsed:       elapsed,
		ElapsedMs:     elapsed.Milliseconds(),
		ContentLength: int64(len(bodyBytes)),
	}

	trimmed := bytes.TrimSpace(bodyBytes)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		var dummy interface{}
		if err := json.Unmarshal(bodyBytes, &dummy); err == nil {
			out.JSON = bodyBytes
			out.IsJSON = true
		}
	}
	if !out.IsJSON {
		out.BodyText = string(bodyBytes)
	}
	return out, nil
}

// MustReadResponseBody is exposed so callers building atypical clients can
// reuse the gzip handling. We keep readResponseBody (lowercase) for the
// internal call sites in FastFetch.
func MustReadResponseBody(resp *http.Response) ([]byte, error) {
	return readResponseBody(resp)
}

// AlgoliaQuery is a small convenience wrapper around FastFetchAPI for the
// common Algolia search shape used by capcar / starterre / aramis-style
// recipes:
//
//	POST https://<appId>-dsn.algolia.net/1/indexes/<index>/query
//	X-Algolia-Application-Id: <appId>
//	X-Algolia-API-Key:        <apiKey>
//	{"params":"<urlencoded form>"}
//
// We construct the params payload from the supplied params map and let
// FastFetchAPI do the actual HTTP work. Returns the parsed Algolia
// response (typically `{ hits: [...], nbHits: N, nbPages: P }`).
func AlgoliaQuery(ctx context.Context, opts AlgoliaOpts) (*APIResponse, error) {
	if opts.AppID == "" || opts.APIKey == "" || opts.Index == "" {
		return nil, errors.New("algolia: appId, apiKey and index are required")
	}
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s-dsn.algolia.net/1/indexes/%s/query", opts.AppID, opts.Index)
	}
	paramsForm := opts.ParamsForm
	if paramsForm == "" && len(opts.Params) > 0 {
		var b strings.Builder
		first := true
		for k, v := range opts.Params {
			if !first {
				b.WriteByte('&')
			}
			first = false
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(v)
		}
		paramsForm = b.String()
	}
	body, err := json.Marshal(map[string]string{"params": paramsForm})
	if err != nil {
		return nil, err
	}

	headers := map[string]string{
		"X-Algolia-Application-Id": opts.AppID,
		"X-Algolia-API-Key":        opts.APIKey,
		"Content-Type":             "application/json",
	}
	for k, v := range opts.ExtraHeaders {
		headers[k] = v
	}

	return FastFetchAPI(ctx, APIRequest{
		Method:  http.MethodPost,
		URL:     endpoint,
		Headers: headers,
		Body:    body,
		Timeout: opts.Timeout,
		Proxy:   opts.Proxy,
	})
}

// AlgoliaOpts groups the Algolia-specific knobs. AppID/APIKey/Index are
// required; Endpoint can be overridden if the site uses a custom DSN.
type AlgoliaOpts struct {
	AppID        string
	APIKey       string
	Index        string
	Endpoint     string
	Params       map[string]string // raw params, joined with `=&`; alternative to ParamsForm
	ParamsForm   string            // pre-built `query=&hitsPerPage=20&page=0` string
	ExtraHeaders map[string]string
	Timeout      time.Duration
	Proxy        string
}

// gzip handler is shared with FastFetch via readResponseBody (defined
// there). We keep this helper here so the file is self-documenting.
var _ = gzip.NewReader // keep import alive in case readResponseBody moves
