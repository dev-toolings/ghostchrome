package mcp

import (
	"context"
	"sync"
	"testing"
)

func TestEmulateConcurrentUpdatesPreserveIndependentAxes(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	t.Setenv("HOME", t.TempDir())
	s := New(Options{Headless: true, TimeoutSec: 15})
	defer s.Close()
	srv := s.Build("test", "0.0.0")
	srv.HandleMessage(context.Background(), mcpToolCall(t, "emulate", "initial", map[string]any{"device": "iphone-14"}))
	requests := [][]byte{
		mcpToolCall(t, "emulate", "ua", map[string]any{"user_agent": "concurrent-test-agent"}),
		mcpToolCall(t, "emulate", "color", map[string]any{"color_scheme": "dark"}),
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, req := range requests {
		wg.Add(1)
		go func(req []byte) { defer wg.Done(); <-start; srv.HandleMessage(context.Background(), req) }(req)
	}
	close(start)
	wg.Wait()
	s.mu.Lock()
	state := s.emulation
	s.mu.Unlock()
	if state.UserAgent != "concurrent-test-agent" || state.ColorScheme != "dark" || state.Width != 390 {
		t.Fatalf("concurrent update lost: %+v", state)
	}
}
