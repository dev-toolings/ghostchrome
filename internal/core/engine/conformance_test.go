package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

func TestSessionDirLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := sessionDir("work")
	if err != nil {
		t.Fatal(err)
	}
	logPath, err := sessionLogPath("work")
	if err != nil {
		t.Fatal(err)
	}
	sock, err := sessionSocketPath("work")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "work" {
		t.Fatalf("dir=%s", dir)
	}
	if filepath.Base(logPath) != "serve.log" {
		t.Fatalf("log=%s", logPath)
	}
	if filepath.Base(sock) != "cdp.sock" {
		t.Fatalf("sock=%s", sock)
	}
	if filepath.Dir(logPath) != dir || filepath.Dir(sock) != dir {
		t.Fatalf("layout dir=%s log=%s sock=%s", dir, logPath, sock)
	}
}

func TestLifecycleArmMatch(t *testing.T) {
	arm := &LifecycleArm{}
	arm.hits = []lifecycleHit{{
		Name:   proto.PageLifecycleEventNameDOMContentLoaded,
		Frame:  "frame-a",
		Loader: "loader-1",
	}}
	if !arm.has(proto.PageLifecycleEventNameDOMContentLoaded, "frame-a", "loader-1") {
		t.Fatal("expected exact match")
	}
	if arm.has(proto.PageLifecycleEventNameDOMContentLoaded, "frame-a", "loader-2") {
		t.Fatal("loader mismatch should fail")
	}
	if !arm.has(proto.PageLifecycleEventNameDOMContentLoaded, "frame-a", "") {
		t.Fatal("empty loader should wildcard")
	}
}

func TestEventHubNilWait(t *testing.T) {
	var hub *EventHub
	if err := hub.WaitLifecycle(proto.PageLifecycleEventNameLoad, "", "", time.Millisecond); err != context.DeadlineExceeded {
		t.Fatalf("err=%v", err)
	}
}

func TestStartEventHubIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)
	h1, err := StartEventHub(page)
	if err != nil || h1 == nil {
		t.Fatalf("start: %v %v", h1, err)
	}
	h2, err := StartEventHub(page)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatal("expected the same hub for the same page")
	}
	h1.Stop()
}

func TestBrowserCloseStopsEventHub(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	b, page := newIsolatedPage(t)
	if _, err := StartEventHub(page); err != nil {
		t.Fatal(err)
	}
	b.Close()
	if HubForPage(page) != nil {
		t.Fatal("browser close retained an event hub")
	}
}

func TestNavigateUsesEventHub(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>hub</title><body><button>Go</button></body>"))
	}))
	t.Cleanup(server.Close)
	_, page := newIsolatedPage(t)
	if _, err := StartEventHub(page); err != nil {
		t.Fatal(err)
	}
	info, err := Navigate(page, server.URL, "domcontentloaded")
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if info == nil || info.URL == "" {
		t.Fatal("empty page info")
	}
}

func TestConformanceCoreLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	n := 20
	if raw := os.Getenv("GHOSTCHROME_CONFORMANCE_OPS"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			n = parsed
		}
	}
	html := `<!doctype html><html><body>
		<button onclick="const state=document.getElementById('state'); state.textContent=state.textContent==='A'?'B':'A'">Go</button>
		<button id="state">A</button><input aria-label=Query>
	</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(server.Close)
	workers := 1
	if n >= 1000 {
		workers = 4
	}
	for worker := 0; worker < workers; worker++ {
		worker := worker
		operations := n / workers
		if worker < n%workers {
			operations++
		}
		t.Run(fmt.Sprintf("worker-%d", worker+1), func(t *testing.T) {
			if workers > 1 {
				t.Parallel()
			}
			runConformanceCoreLoop(t, server.URL, operations)
		})
	}
}

func runConformanceCoreLoop(t *testing.T, serverURL string, n int) {
	t.Helper()
	b, page := newIsolatedPage(t)
	rt := NewRuntime(b)
	if hub := rt.AttachEvents(page); hub == nil {
		t.Fatal("expected event hub")
	}
	ps := rt.PageSession(page)
	if _, err := Navigate(page, serverURL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	snap, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	failures := 0
	for i := 0; i < n; i++ {
		if err := ps.ClickRef("@1", snap); err != nil {
			failures++
			t.Logf("click %d: %v", i, err)
			continue
		}
		_, current, err := CaptureMutation(b, page, snap)
		if err != nil {
			failures++
			t.Logf("diff %d: %v", i, err)
			continue
		}
		next, err := BuildSnapshot(page, current)
		if err != nil {
			failures++
			t.Logf("snapshot %d: %v", i, err)
			continue
		}
		snap = next
	}
	if failures > 0 {
		t.Fatalf("%d/%d core ops failed", failures, n)
	}
}

func TestConformanceSoak(t *testing.T) {
	if os.Getenv("GHOSTCHROME_SOAK") != "1" {
		t.Skip("set GHOSTCHROME_SOAK=1 to run the soak loop")
	}
	if os.Getenv("GHOSTCHROME_CONFORMANCE_OPS") == "" {
		t.Setenv("GHOSTCHROME_CONFORMANCE_OPS", "10000")
	}
	TestConformanceCoreLoop(t)
}

func TestConformanceDuration(t *testing.T) {
	if os.Getenv("GHOSTCHROME_SOAK") != "1" {
		t.Skip("set GHOSTCHROME_SOAK=1 to run the duration soak")
	}
	raw := strings.TrimSpace(os.Getenv("GHOSTCHROME_SOAK_DURATION"))
	if raw == "" {
		raw = "8h"
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		t.Fatalf("GHOSTCHROME_SOAK_DURATION=%q: %v", raw, err)
	}
	deadline := time.Now().Add(d)
	html := "<!doctype html><html><body><button>Go</button><input aria-label=Query></body></html>"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(server.Close)
	b, page := newIsolatedPage(t)
	rt := NewRuntime(b)
	if hub := rt.AttachEvents(page); hub == nil {
		t.Fatal("expected event hub")
	}
	ps := rt.PageSession(page)
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	snap, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	ops, failures := 0, 0
	lastBeat := time.Now()
	for time.Now().Before(deadline) {
		ops++
		if time.Since(lastBeat) >= time.Minute {
			t.Logf("heartbeat ops=%d failures=%d remaining=%s", ops, failures, time.Until(deadline).Round(time.Second))
			lastBeat = time.Now()
		}
		if err := ps.ClickRef("@1", snap); err != nil {
			failures++
			t.Logf("click %d: %v", ops, err)
			continue
		}
		if _, _, err := CaptureMutation(b, page, snap); err != nil {
			failures++
			t.Logf("diff %d: %v", ops, err)
		}
	}
	t.Logf("duration soak ran %d ops in %s, failures=%d", ops, d, failures)
	if ops == 0 {
		t.Fatal("no ops completed")
	}
	if failures > 0 {
		t.Fatalf("%d/%d core ops failed", failures, ops)
	}
}

func TestAttachEventsRetargetsHub(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	b, page := newIsolatedPage(t)
	rt := NewRuntime(b)
	h1 := rt.AttachEvents(page)
	if h1 == nil {
		t.Fatal("expected hub")
	}
	second, err := NewTab(b.RodBrowser(), "about:blank")
	if err != nil {
		t.Fatalf("new tab: %v", err)
	}
	h2 := rt.AttachEvents(second)
	if h2 == nil {
		t.Fatal("expected retargeted hub")
	}
	if h1 == h2 {
		t.Fatal("expected a new hub for the new page")
	}
	if HubForPage(page) != nil {
		t.Fatal("old page hub should be stopped")
	}
	if HubForPage(second) != h2 {
		t.Fatal("new page hub mismatch")
	}
}
