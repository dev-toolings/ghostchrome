package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultDiscoverPorts is the list of common Chrome remote debugging ports
// scanned by DiscoverCDP when no explicit port is supplied.
var DefaultDiscoverPorts = []int{9222, 9223, 9224, 9225, 9226, 9227, 9228, 9229}

// DiscoverCDP probes 127.0.0.1 on the given ports for a running Chrome
// exposing /json/version, and returns the first webSocketDebuggerUrl found.
// When ports is nil/empty, DefaultDiscoverPorts is used. Returns an error
// when no endpoint responds within timeout.
func DiscoverCDP(ports []int, timeout time.Duration) (string, error) {
	if len(ports) == 0 {
		ports = DefaultDiscoverPorts
	}
	if timeout <= 0 {
		timeout = 400 * time.Millisecond
	}

	type result struct {
		ws  string
		err error
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Indexed slots keep the result order aligned with the input ports
	// slice so the caller always sees the lowest-numbered responding port
	// first — regardless of which probe goroutine finishes first.
	results := make([]result, len(ports))
	var wg sync.WaitGroup
	wg.Add(len(ports))
	for i, p := range ports {
		i, p := i, p
		go func() {
			defer wg.Done()
			version, err := probePortVersion(ctx, p)
			ws := ""
			if version != nil {
				ws = version.WebSocketDebuggerURL
			}
			results[i] = result{ws: ws, err: err}
		}()
	}
	wg.Wait()

	var lastErr error
	for _, r := range results {
		if r.ws != "" {
			return r.ws, nil
		}
		if r.err != nil {
			lastErr = r.err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no Chrome found on default debug ports")
	}
	return "", fmt.Errorf("discover cdp: %w", lastErr)
}

type cdpVersionInfo struct {
	Browser              string `json:"Browser"`
	UserAgent            string `json:"User-Agent"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func probePortVersion(ctx context.Context, port int) (*cdpVersionInfo, error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	d := net.Dialer{Timeout: 200 * time.Millisecond}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil // closed port is not an error worth surfacing
	}
	_ = conn.Close()

	url := "http://" + addr + "/json/version"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil
	}
	var payload cdpVersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.WebSocketDebuggerURL == "" {
		return nil, nil
	}
	return &payload, nil
}

// DiscoverCDPChannel probes local Chrome remote-debugging ports for a browser
// matching a playwright-cli channel name. It attaches only to already running
// browsers with remote debugging enabled.
func DiscoverCDPChannel(channel string, ports []int, timeout time.Duration) (string, error) {
	family, err := channelFamily(channel)
	if err != nil {
		return "", err
	}
	if len(ports) == 0 {
		ports = DefaultDiscoverPorts
	}
	if timeout <= 0 {
		timeout = 800 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var lastErr error
	for _, port := range ports {
		version, err := probePortVersion(ctx, port)
		if err != nil {
			lastErr = err
			continue
		}
		if version == nil {
			continue
		}
		if versionMatchesChannelFamily(version, family) {
			return version.WebSocketDebuggerURL, nil
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("discover cdp channel %q: %w", channel, lastErr)
	}
	return "", fmt.Errorf("discover cdp channel %q: no matching browser found on local debug ports; enable remote debugging in the target browser", channel)
}

func channelFamily(channel string) (string, error) {
	switch strings.TrimSpace(channel) {
	case "chrome", "chrome-beta", "chrome-dev", "chrome-canary":
		return "chrome", nil
	case "msedge", "msedge-beta", "msedge-dev", "msedge-canary":
		return "edge", nil
	default:
		return "", fmt.Errorf("unsupported cdp channel %q (supported: chrome, chrome-beta, chrome-dev, chrome-canary, msedge, msedge-beta, msedge-dev, msedge-canary)", channel)
	}
}

func versionMatchesChannelFamily(version *cdpVersionInfo, family string) bool {
	if version == nil {
		return false
	}
	haystack := strings.ToLower(version.Browser + " " + version.UserAgent)
	switch family {
	case "chrome":
		return (strings.Contains(haystack, "chrome/") || strings.Contains(haystack, "chromium/")) &&
			!strings.Contains(haystack, "edg/")
	case "edge":
		return strings.Contains(haystack, "edg/") || strings.Contains(haystack, "edge/")
	default:
		return false
	}
}

// ProbeCDPEndpoint checks that a CDP HTTP or WebSocket URL still answers
// /json/version. It never opens a Rod session and therefore never sends
// Browser.close, which would kill an external Chrome.
func ProbeCDPEndpoint(raw string, timeout time.Duration) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("cdp endpoint is empty")
	}
	if timeout <= 0 {
		timeout = 800 * time.Millisecond
	}
	versionURL, err := cdpVersionURL(raw)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", versionURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned HTTP %d", versionURL, resp.StatusCode)
	}
	var payload cdpVersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("%s did not return webSocketDebuggerUrl", versionURL)
	}
	return payload.WebSocketDebuggerURL, nil
}

func cdpVersionURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse cdp endpoint: %w", err)
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
		return strings.TrimRight(raw, "/") + "/json/version", nil
	default:
		return "", fmt.Errorf("unsupported cdp endpoint scheme %q (use http(s) or ws(s))", u.Scheme)
	}
	u.Path = "/json/version"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// ResolveCDPEndpoint returns a browser WebSocket debugger URL for a CDP
// endpoint. ws:// and wss:// inputs are already resolved; http(s) inputs are
// probed via /json/version.
func ResolveCDPEndpoint(raw string, timeout time.Duration) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("cdp endpoint is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse cdp endpoint: %w", err)
	}
	switch u.Scheme {
	case "ws", "wss":
		return raw, nil
	case "http", "https":
		if timeout <= 0 {
			timeout = 800 * time.Millisecond
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		versionURL := strings.TrimRight(raw, "/") + "/json/version"
		req, err := http.NewRequestWithContext(ctx, "GET", versionURL, nil)
		if err != nil {
			return "", err
		}
		resp, err := (&http.Client{Timeout: timeout}).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("%s returned HTTP %d", versionURL, resp.StatusCode)
		}
		var payload cdpVersionInfo
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return "", err
		}
		if payload.WebSocketDebuggerURL == "" {
			return "", fmt.Errorf("%s did not return webSocketDebuggerUrl", versionURL)
		}
		return payload.WebSocketDebuggerURL, nil
	default:
		return "", fmt.Errorf("unsupported cdp endpoint scheme %q (use http(s) or ws(s))", u.Scheme)
	}
}
