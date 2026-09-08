package mcp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestMCPFunctionalMatrix10(t *testing.T) {
	if testing.Short() {
		t.Skip("requires real Chrome lifecycle scenarios")
	}

	cases := []string{
		"lazy_launch_alive",
		"prewarm_launch_alive",
		"recent_idle_keeps_browser",
		"expired_idle_reaps_browser",
		"idle_reap_then_relaunch",
		"crash_then_relaunch",
		"terminal_close_rejects_ensure",
		"terminal_close_rejects_prewarm",
		"concurrent_terminal_close",
		"with_page_runs_callback",
	}
	if len(cases) != 10 {
		t.Fatalf("MCP functional cases = %d, want 10", len(cases))
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			runMCPFunctionalCase(t, name)
		})
	}
}

func runMCPFunctionalCase(t *testing.T, name string) {
	t.Helper()
	s := New(Options{Headless: true, TimeoutSec: 10, IdleTimeout: time.Second})
	defer s.Close()
	launch := func() (*engine.Browser, *rod.Page) {
		s.mu.Lock()
		b, p, err := s.ensurePageLocked()
		s.mu.Unlock()
		if err != nil {
			t.Fatalf("ensure page: %v", err)
		}
		return b, p
	}

	switch name {
	case "lazy_launch_alive":
		b, _ := launch()
		if !b.Alive(time.Second) {
			t.Fatal("lazy browser not alive")
		}
	case "prewarm_launch_alive":
		s.PrewarmAsync()
		b, _ := launch()
		if !b.Alive(time.Second) {
			t.Fatal("prewarmed browser not alive")
		}
	case "recent_idle_keeps_browser":
		b, _ := launch()
		s.reapIfIdle()
		s.mu.Lock()
		got := s.browser
		s.mu.Unlock()
		if got != b {
			t.Fatal("recent browser was reaped")
		}
	case "expired_idle_reaps_browser":
		launch()
		s.mu.Lock()
		s.lastActivity = time.Now().Add(-2 * time.Second)
		s.mu.Unlock()
		s.reapIfIdle()
		s.mu.Lock()
		got := s.browser
		s.mu.Unlock()
		if got != nil {
			t.Fatal("expired browser was not reaped")
		}
	case "idle_reap_then_relaunch":
		b1, _ := launch()
		s.mu.Lock()
		s.lastActivity = time.Now().Add(-2 * time.Second)
		s.mu.Unlock()
		s.reapIfIdle()
		b2, _ := launch()
		if b1 == b2 {
			t.Fatal("idle reap did not relaunch")
		}
	case "crash_then_relaunch":
		b1, _ := launch()
		_ = b1.RodBrowser().Close()
		deadline := time.Now().Add(5 * time.Second)
		for b1.Alive(100*time.Millisecond) && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
		b2, _ := launch()
		if b1 == b2 {
			t.Fatal("crash did not relaunch")
		}
	case "terminal_close_rejects_ensure":
		launch()
		s.Close()
		s.mu.Lock()
		_, _, err := s.ensurePageLocked()
		s.mu.Unlock()
		if !errors.Is(err, errServerClosed) {
			t.Fatalf("ensure after close = %v", err)
		}
	case "terminal_close_rejects_prewarm":
		launch()
		s.Close()
		s.PrewarmAsync()
		s.mu.Lock()
		got := s.browser
		s.mu.Unlock()
		if got != nil {
			t.Fatal("prewarm relaunched after close")
		}
	case "concurrent_terminal_close":
		launch()
		var wg sync.WaitGroup
		wg.Add(8)
		for range 8 {
			go func() { defer wg.Done(); s.Close() }()
		}
		wg.Wait()
	case "with_page_runs_callback":
		called := false
		result, err := s.withPage(context.Background(), func(_ *engine.Browser, p *rod.Page) (*mcpgo.CallToolResult, error) {
			called = true
			if _, err := p.Info(); err != nil {
				return nil, err
			}
			return mcpgo.NewToolResultText("ok"), nil
		})
		if err != nil || result == nil || !called {
			t.Fatalf("withPage result=%v called=%v err=%v", result, called, err)
		}
	default:
		t.Fatalf("unknown MCP case %q", name)
	}
}

func TestServerCloseIsTerminalBeforeLaunch(t *testing.T) {
	s := New(Options{Headless: true, TimeoutSec: 10})
	s.Close()
	s.PrewarmAsync()

	s.mu.Lock()
	_, _, err := s.ensurePageLocked()
	s.mu.Unlock()
	if !errors.Is(err, errServerClosed) {
		t.Fatalf("ensure after Close error = %v, want %v", err, errServerClosed)
	}

	called := false
	result, err := s.withPage(context.Background(), func(*engine.Browser, *rod.Page) (*mcpgo.CallToolResult, error) {
		called = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("withPage after Close: %v", err)
	}
	if called {
		t.Fatal("withPage callback ran after terminal Close")
	}
	if result == nil || !result.IsError {
		t.Fatalf("withPage after Close result = %#v, want MCP error", result)
	}
}

func TestServerCloseAfterPrewarmCannotRelaunch(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	s := New(Options{Headless: true, TimeoutSec: 10})
	s.PrewarmAsync()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.mu.Lock()
		launched := s.browser != nil
		s.mu.Unlock()
		if launched {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prewarm did not launch Chrome")
		}
		time.Sleep(10 * time.Millisecond)
	}

	s.Close()
	s.PrewarmAsync()
	s.mu.Lock()
	closed, browser := s.closed, s.browser
	s.mu.Unlock()
	if !closed || browser != nil {
		t.Fatalf("terminal state after Close: closed=%v browser=%p", closed, browser)
	}
}

// TestReapIfIdleNoBrowserIsNoop guards the cheap path: with no browser held
// (or reaping disabled) the reaper must never panic and must leave state alone.
// Runs without Chrome.
func TestReapIfIdleNoBrowserIsNoop(t *testing.T) {
	s := New(Options{IdleTimeout: time.Minute})
	s.reapIfIdle() // browser == nil, lastActivity zero
	if s.browser != nil {
		t.Fatal("reapIfIdle materialized a browser out of nothing")
	}

	// Disabled reaper never touches anything, even with a stale lastActivity.
	s2 := New(Options{IdleTimeout: 0})
	s2.lastActivity = time.Now().Add(-time.Hour)
	s2.reapIfIdle()
}

// TestReapIfIdleReleasesThenRelaunches proves the fix: after IdleTimeout of no
// activity the held Chrome is released, but the server stays usable and the
// next ensurePageLocked spins up a fresh browser on demand.
func TestReapIfIdleReleasesThenRelaunches(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	s := New(Options{Headless: true, TimeoutSec: 15, IdleTimeout: time.Second})
	defer s.Close()

	s.mu.Lock()
	b1, _, err := s.ensurePageLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}

	// Recent activity: the reaper must leave the browser alone.
	s.reapIfIdle()
	s.mu.Lock()
	stillHeld := s.browser
	s.mu.Unlock()
	if stillHeld == nil {
		t.Fatal("reaped a browser that was just active")
	}

	// Simulate the timeout having elapsed, then reap.
	s.mu.Lock()
	s.lastActivity = time.Now().Add(-2 * time.Second)
	s.mu.Unlock()
	s.reapIfIdle()
	s.mu.Lock()
	reaped := s.browser == nil && s.snapshot == nil
	s.mu.Unlock()
	if !reaped {
		t.Fatal("idle browser was not released")
	}

	// Server is still alive: the next call relaunches Chrome on demand.
	s.mu.Lock()
	b2, page2, err := s.ensurePageLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("relaunch after idle reap: %v", err)
	}
	if b2 == b1 {
		t.Fatal("expected a fresh browser after reap, got the released handle")
	}
	if _, err := page2.Info(); err != nil {
		t.Fatalf("relaunched page unusable: %v", err)
	}
}

func TestReapIfIdleSkipsAttachedAndHeaded(t *testing.T) {
	now := time.Now().Add(-time.Hour)

	attached := New(Options{Headless: true, Connect: "ws://127.0.0.1:9222", IdleTimeout: time.Second})
	attached.browser = &engine.Browser{}
	attached.lastActivity = now
	attached.reapIfIdle()
	if attached.browser == nil {
		t.Fatal("attached chrome must not be reaped")
	}

	headed := New(Options{Headless: false, IdleTimeout: time.Second})
	headed.browser = &engine.Browser{}
	headed.lastActivity = now
	headed.reapIfIdle()
	if headed.browser == nil {
		t.Fatal("headed chrome must not be reaped")
	}
}
