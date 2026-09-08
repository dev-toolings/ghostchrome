package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
)

func TestAliveNilBrowser(t *testing.T) {
	var b *engine.Browser
	if b.Alive(time.Second) {
		t.Error("nil Browser reported alive")
	}
}

// TestEnsurePageRelaunchesAfterBrowserDeath reproduces the wedged-singleton
// bug: Chrome dies under the MCP server, and before the fix every tool call
// failed forever with "context deadline exceeded" because ensurePageLocked
// trusted any non-nil handle. The fix probes liveness and relaunches.
func TestEnsurePageRelaunchesAfterBrowserDeath(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	s := New(Options{Headless: true, TimeoutSec: 15})
	defer s.Close()

	s.mu.Lock()
	b1, _, err := s.ensurePageLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	if !b1.Alive(2 * time.Second) {
		t.Fatal("freshly launched browser reported dead")
	}

	// Kill Chrome out from under the server, leaving the stale handles in
	// place — exactly the state a crash leaves behind.
	_ = b1.RodBrowser().Close()
	deadline := time.Now().Add(5 * time.Second)
	for b1.Alive(500*time.Millisecond) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if b1.Alive(500 * time.Millisecond) {
		t.Fatal("browser still alive after kill; cannot exercise recovery")
	}

	s.mu.Lock()
	b2, page2, err := s.ensurePageLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("relaunch after browser death: %v", err)
	}
	if b2 == b1 {
		t.Fatal("expected a fresh browser after death, got the stale handle")
	}
	if _, err := page2.Info(); err != nil {
		t.Fatalf("relaunched page unusable: %v", err)
	}
	if s.snapshot != nil {
		t.Error("stale snapshot survived relaunch; refs would resolve into the dead browser")
	}
}

// TestEnsurePageReacquiresClosedTarget keeps the browser connection alive when
// only the active target disappeared. This is the failure mode that a
// browser-level Version ping cannot detect: the old implementation returned
// the cached rod.Page forever, so every following tool call targeted a closed
// page and eventually looked like a Chrome restart.
func TestEnsurePageReacquiresClosedTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	s := New(Options{Headless: true, TimeoutSec: 15})
	defer s.Close()

	s.mu.Lock()
	b1, page1, err := s.ensurePageLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	targetID := page1.TargetID
	if err := page1.Close(); err != nil {
		t.Fatalf("close active target: %v", err)
	}
	if !b1.Alive(2 * time.Second) {
		t.Fatal("closing a target unexpectedly killed Chrome")
	}

	s.mu.Lock()
	b2, page2, err := s.ensurePageLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("recover after target close: %v", err)
	}
	if b2 != b1 {
		t.Fatal("closed target caused an unnecessary browser relaunch")
	}
	if page2 == page1 || page2.TargetID == targetID {
		t.Fatalf("recovery returned the closed target: old=%s new=%s", targetID, page2.TargetID)
	}
	if _, err := page2.Info(); err != nil {
		t.Fatalf("reacquired page unusable: %v", err)
	}
	d := s.Diagnostics()
	if d.RecoveryCount == 0 || !strings.Contains(d.LastRecovery, "page target") {
		t.Fatalf("diagnostics after target recovery = %#v", d)
	}
	if d.TargetID != string(page2.TargetID) {
		t.Fatalf("diagnostics target=%q, want %q", d.TargetID, page2.TargetID)
	}
}

func TestDiagnosticsDoesNotProbeChrome(t *testing.T) {
	s := New(Options{Connect: "ws://127.0.0.1:9223"})
	d := s.Diagnostics()
	if d.Connect != "ws://127.0.0.1:9223" {
		t.Fatalf("diagnostics connect=%q", d.Connect)
	}
	if d.BrowserHeld || d.PageHeld || d.RecoveryCount != 0 {
		t.Fatalf("unexpected empty-server diagnostics: %#v", d)
	}
}
