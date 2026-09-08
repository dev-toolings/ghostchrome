package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// TestSnapshotStableCancellationUnblocksTabs reproduces the production wedge:
// a fetch-based SSE stream keeps Rod's WaitStable request-idle waiter open.
// The MCP cancellation notification must cancel the Rod-bound page context so
// the snapshot releases Server.mu and the next tool call can run.
func TestSnapshotStableCancellationUnblocksTabs(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	cssRequested := make(chan struct{}, 1)
	fetchOpened := make(chan struct{}, 1)
	releaseCSS := make(chan struct{})
	releaseFetch := make(chan struct{})
	var releaseCSSOnce sync.Once
	var releaseFetchOnce sync.Once
	closeCSS := func() { releaseCSSOnce.Do(func() { close(releaseCSS) }) }
	closeFetch := func() { releaseFetchOnce.Do(func() { close(releaseFetch) }) }
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<!doctype html><link rel="stylesheet" href="/gate.css"><script>
				window.addEventListener("load", () => fetch("/events"));
			</script><p>ready</p>`)
		case "/gate.css":
			select {
			case cssRequested <- struct{}{}:
			default:
			}
			<-releaseCSS
			w.Header().Set("Content-Type", "text/css")
		case "/events":
			// This is intentionally fetched instead of constructed through
			// EventSource: Rod excludes native EventSource from network-idle,
			// while fetch-based SSE is the persistent request this regression covers.
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			select {
			case fetchOpened <- struct{}{}:
			default:
			}
			select {
			case <-releaseFetch:
			case <-r.Context().Done():
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer pageServer.Close()

	s := New(Options{Headless: true, TimeoutSec: 15})
	// Launch before starting the blocked request so a failing regression test can
	// force-close this exact browser and cannot leave a Chrome process behind.
	s.mu.Lock()
	browser, _, err := s.ensurePageLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("launch browser: %v", err)
	}
	defer closeFetch()
	defer closeCSS()
	defer s.Close()

	server := s.Build("test", "0.0.0")
	snapshotRequest := mcpToolCall(t, "snapshot", "snapshot-stable", map[string]any{
		"url":  pageServer.URL,
		"wait": "stable",
	})
	snapshotDone := make(chan mcpgo.JSONRPCMessage, 1)
	go func() {
		snapshotDone <- server.HandleMessage(context.Background(), snapshotRequest)
	}()

	select {
	case <-cssRequested:
	case <-time.After(5 * time.Second):
		browser.Close()
		t.Fatal("stylesheet gate was not requested")
	}
	// WaitStable is now listening before the document load event can fire.
	closeCSS()
	select {
	case <-fetchOpened:
	case <-time.After(5 * time.Second):
		browser.Close()
		t.Fatal("fetch-based SSE stream was not opened")
	}
	// WaitStable must still be waiting while the fetch remains open. A shorter
	// wait than Rod's 500ms settle period could pass without exercising it.
	select {
	case response := <-snapshotDone:
		browser.Close()
		t.Fatalf("snapshot returned before cancellation while SSE was open: %s", marshalMCPMessage(t, response))
	case <-time.After(750 * time.Millisecond):
	}

	if response := server.HandleMessage(context.Background(), mcpCancelled("snapshot-stable")); response != nil {
		browser.Close()
		t.Fatalf("cancellation notification returned a response: %s", marshalMCPMessage(t, response))
	}

	select {
	case response := <-snapshotDone:
		if payload := marshalMCPMessage(t, response); !bytes.Contains(payload, []byte("context canceled")) {
			browser.Close()
			t.Fatalf("cancelled snapshot response missing context cancellation: %s", payload)
		}
	case <-time.After(3 * time.Second):
		browser.Close()
		t.Fatal("cancelled snapshot did not release the server mutex")
	}

	tabsRequest := mcpToolCall(t, "tabs", "tabs-after-cancel", nil)
	tabsDone := make(chan mcpgo.JSONRPCMessage, 1)
	go func() {
		tabsDone <- server.HandleMessage(context.Background(), tabsRequest)
	}()
	select {
	case response := <-tabsDone:
		if response == nil {
			t.Fatal("tabs returned no MCP response after cancellation")
		}
		if payload := marshalMCPMessage(t, response); bytes.Contains(payload, []byte("context canceled")) {
			t.Fatalf("tabs inherited the cancelled request context: %s", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tabs remained blocked after snapshot cancellation")
	}
}

func mcpToolCall(t *testing.T, name, id string, args map[string]any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})
	if err != nil {
		t.Fatalf("marshal %s request: %v", name, err)
	}
	return payload
}

func mcpCancelled(id string) json.RawMessage {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/cancelled",
		"params":  map[string]any{"requestId": id},
	})
	return payload
}

func marshalMCPMessage(t *testing.T, message mcpgo.JSONRPCMessage) []byte {
	t.Helper()
	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal MCP response: %v", err)
	}
	return payload
}
