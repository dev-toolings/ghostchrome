package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ReplayInput is the agent-facing payload for replaying an HTTP request
// captured by Sniff. All fields are JSON-friendly and trivially mutable.
type ReplayInput struct {
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers,omitempty"`
	Body         string            `json:"body,omitempty"`
	TimeoutMs    int               `json:"timeout_ms,omitempty"`
	MaxBodyBytes int               `json:"max_body_bytes,omitempty"`
}

// ReplayResult is what the agent sees back: status + truncated body +
// optional decoded top-level array stats so the LLM can decide whether
// the params it just sent yielded the expected list.
type ReplayResult struct {
	Status     int               `json:"status"`
	URL        string            `json:"url"`
	Headers    map[string]string `json:"headers,omitempty"`
	MimeType   string            `json:"mime_type,omitempty"`
	Body       string            `json:"body,omitempty"`
	BodyBytes  int               `json:"body_bytes"`
	DurationMs int64             `json:"duration_ms"`
	ItemCount  int               `json:"item_count,omitempty"`
	ItemPath   string            `json:"item_path,omitempty"`
	SampleItem string            `json:"sample_item,omitempty"`
	Truncated  bool              `json:"truncated,omitempty"`
}

// noiseHeaders are removed from the replayed request because they are
// either set automatically by Go's http client (Host, Content-Length) or
// browser-specific noise that would fingerprint us as a bot.
var noiseHeaders = map[string]bool{
	":method":                     true,
	":path":                       true,
	":authority":                  true,
	":scheme":                     true,
	"host":                        true,
	"content-length":              true,
	"sec-ch-ua-platform-version":  true,
	"sec-ch-ua-arch":              true,
	"sec-ch-ua-bitness":           true,
	"sec-ch-ua-full-version":      true,
	"sec-ch-ua-full-version-list": true,
	"sec-ch-ua-model":             true,
}

// HTTPClient is package-level for test injection.
var HTTPClient = &http.Client{Timeout: 15 * time.Second}

// Replay re-issues an HTTP request and returns the parsed response.
// Pure HTTP — no browser, no Chrome, no fingerprint variance. The agent
// uses this after Sniff to query the discovered backend with mutated
// params (different brand, page, filters).
func Replay(ctx context.Context, in ReplayInput) (*ReplayResult, error) {
	if in.URL == "" {
		return nil, fmt.Errorf("url is required")
	}
	method := strings.ToUpper(in.Method)
	if method == "" {
		method = http.MethodGet
	}
	if in.MaxBodyBytes <= 0 {
		in.MaxBodyBytes = 256 * 1024
	}

	var bodyReader io.Reader
	if in.Body != "" {
		bodyReader = strings.NewReader(in.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, in.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	for k, v := range in.Headers {
		if noiseHeaders[strings.ToLower(k)] {
			continue
		}
		req.Header.Set(k, v)
	}
	// Default Content-Type for POST + JSON body when caller didn't set one.
	if method == http.MethodPost && req.Header.Get("Content-Type") == "" && looksLikeJSON(in.Body) {
		req.Header.Set("Content-Type", "application/json")
	}

	client := HTTPClient
	if in.TimeoutMs > 0 {
		client = &http.Client{Timeout: time.Duration(in.TimeoutMs) * time.Millisecond}
	}

	t0 := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, int64(in.MaxBodyBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	truncated := len(bodyBytes) > in.MaxBodyBytes
	if truncated {
		bodyBytes = bodyBytes[:in.MaxBodyBytes]
	}
	bodyStr := string(bodyBytes)

	out := &ReplayResult{
		Status:     resp.StatusCode,
		URL:        resp.Request.URL.String(),
		MimeType:   resp.Header.Get("Content-Type"),
		BodyBytes:  len(bodyBytes),
		Body:       bodyStr,
		DurationMs: time.Since(t0).Milliseconds(),
		Truncated:  truncated,
		Headers:    map[string]string{},
	}
	for k, v := range resp.Header {
		if len(v) > 0 {
			out.Headers[k] = v[0]
		}
	}
	if path, count, sample := findTopLevelArray(bodyStr, in.MaxBodyBytes); count > 0 {
		out.ItemCount = count
		out.ItemPath = path
		out.SampleItem = sample
	}
	return out, nil
}

// looksLikeJSON returns true when the body starts with { or [ (cheap
// content-type sniff for default header injection).
func looksLikeJSON(s string) bool {
	t := strings.TrimLeft(s, " \t\r\n")
	return strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")
}

// MutateAlgoliaParams is a small convenience for the most common case:
// the body is `{"params": "url-encoded-string"}` (Algolia wire format)
// and the agent wants to override one or more keys (filters, hitsPerPage,
// page, ...). Returns the new body string ready to drop in ReplayInput.Body.
func MutateAlgoliaParams(originalBody string, overrides map[string]string) (string, error) {
	var wrap map[string]any
	if err := json.Unmarshal([]byte(originalBody), &wrap); err != nil {
		return "", fmt.Errorf("body is not JSON: %w", err)
	}
	rawParams, _ := wrap["params"].(string)
	parsed, err := parseURLEncoded(rawParams)
	if err != nil {
		return "", fmt.Errorf("params not url-encoded: %w", err)
	}
	for k, v := range overrides {
		parsed[k] = []string{v}
	}
	wrap["params"] = encodeURLValues(parsed)
	out, err := json.Marshal(wrap)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func parseURLEncoded(s string) (map[string][]string, error) {
	out := map[string][]string{}
	if s == "" {
		return out, nil
	}
	parts := strings.Split(s, "&")
	for _, p := range parts {
		if p == "" {
			continue
		}
		eq := strings.IndexByte(p, '=')
		var k, v string
		if eq < 0 {
			k = p
		} else {
			k = p[:eq]
			v = p[eq+1:]
		}
		dk, _ := pathUnescape(k)
		dv, _ := pathUnescape(v)
		out[dk] = append(out[dk], dv)
	}
	return out, nil
}

func encodeURLValues(m map[string][]string) string {
	var sb strings.Builder
	first := true
	for k, vs := range m {
		for _, v := range vs {
			if !first {
				sb.WriteByte('&')
			}
			first = false
			sb.WriteString(pathEscape(k))
			sb.WriteByte('=')
			sb.WriteString(pathEscape(v))
		}
	}
	return sb.String()
}

// Reuse net/url helpers without pulling another import.
func pathEscape(s string) string {
	var sb strings.Builder
	for _, r := range []byte(s) {
		if isUnreserved(r) {
			sb.WriteByte(r)
		} else {
			sb.WriteByte('%')
			sb.WriteString(fmt.Sprintf("%02X", r))
		}
	}
	return sb.String()
}

func pathUnescape(s string) (string, error) {
	var out bytes.Buffer
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '+':
			out.WriteByte(' ')
		case '%':
			if i+2 >= len(s) {
				return "", fmt.Errorf("truncated %% at %d", i)
			}
			var b byte
			if _, err := fmt.Sscanf(s[i+1:i+3], "%02x", &b); err != nil {
				return "", err
			}
			out.WriteByte(b)
			i += 2
		default:
			out.WriteByte(s[i])
		}
	}
	return out.String(), nil
}

func isUnreserved(b byte) bool {
	return (b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9') ||
		b == '-' || b == '_' || b == '.' || b == '~'
}
