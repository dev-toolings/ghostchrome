package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestResolveCDPEndpointHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/browser/test",
		})
	}))
	defer srv.Close()

	got, err := ResolveCDPEndpoint(srv.URL, time.Second)
	if err != nil {
		t.Fatalf("ResolveCDPEndpoint: %v", err)
	}
	if got != "ws://127.0.0.1:9222/devtools/browser/test" {
		t.Fatalf("unexpected ws url %q", got)
	}
}

func TestResolveCDPEndpointWS(t *testing.T) {
	const endpoint = "ws://127.0.0.1:9222/devtools/browser/test"
	got, err := ResolveCDPEndpoint(endpoint, time.Second)
	if err != nil {
		t.Fatalf("ResolveCDPEndpoint: %v", err)
	}
	if got != endpoint {
		t.Fatalf("unexpected ws url %q", got)
	}
}

func TestProbeCDPEndpointFromWebsocketURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/browser/live",
		})
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ws := "ws://" + u.Host + "/devtools/browser/live"
	got, err := ProbeCDPEndpoint(ws, time.Second)
	if err != nil {
		t.Fatalf("ProbeCDPEndpoint: %v", err)
	}
	if got != "ws://127.0.0.1:9222/devtools/browser/live" {
		t.Fatalf("unexpected ws url %q", got)
	}
}

func TestCDPVersionURL(t *testing.T) {
	got, err := cdpVersionURL("ws://127.0.0.1:9222/devtools/browser/abc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:9222/json/version" {
		t.Fatalf("got %q", got)
	}
}

func TestChannelFamily(t *testing.T) {
	tests := map[string]string{
		"chrome":        "chrome",
		"chrome-beta":   "chrome",
		"chrome-dev":    "chrome",
		"chrome-canary": "chrome",
		"msedge":        "edge",
		"msedge-beta":   "edge",
		"msedge-dev":    "edge",
		"msedge-canary": "edge",
	}
	for channel, want := range tests {
		got, err := channelFamily(channel)
		if err != nil {
			t.Fatalf("channelFamily(%q): %v", channel, err)
		}
		if got != want {
			t.Fatalf("channelFamily(%q) = %q, want %q", channel, got, want)
		}
	}
	if _, err := channelFamily("firefox"); err == nil {
		t.Fatal("expected unsupported channel error")
	}
}

func TestVersionMatchesChannelFamily(t *testing.T) {
	chrome := &cdpVersionInfo{Browser: "Chrome/126.0", UserAgent: "Mozilla/5.0 Chrome/126.0 Safari/537.36"}
	edge := &cdpVersionInfo{Browser: "Chrome/126.0", UserAgent: "Mozilla/5.0 Chrome/126.0 Safari/537.36 Edg/126.0"}
	if !versionMatchesChannelFamily(chrome, "chrome") {
		t.Fatal("chrome version should match chrome family")
	}
	if versionMatchesChannelFamily(edge, "chrome") {
		t.Fatal("edge user-agent should not match chrome family")
	}
	if !versionMatchesChannelFamily(edge, "edge") {
		t.Fatal("edge version should match edge family")
	}
}

func TestDiscoverCDPChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/126.0",
			"User-Agent":           "Mozilla/5.0 Chrome/126.0 Safari/537.36",
			"webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/browser/test",
		})
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(u.Host, ":")
	port, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		t.Fatal(err)
	}

	got, err := DiscoverCDPChannel("chrome", []int{port}, time.Second)
	if err != nil {
		t.Fatalf("DiscoverCDPChannel: %v", err)
	}
	if got != "ws://127.0.0.1:9222/devtools/browser/test" {
		t.Fatalf("unexpected ws url %q", got)
	}
}
