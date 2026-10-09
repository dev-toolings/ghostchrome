package engine

import (
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

func TestPageIdleTimeoutEnv(t *testing.T) {
	cases := map[string]time.Duration{
		"":    DefaultPageIdleTimeout,
		"0":   0,
		"off": 0,
		"90s": 90 * time.Second,
		"120": 2 * time.Minute,
		"bad": DefaultPageIdleTimeout,
	}
	for in, want := range cases {
		t.Setenv("GHOSTCHROME_PAGE_IDLE_TIMEOUT", in)
		if got := PageIdleTimeout(); got != want {
			t.Errorf("PageIdleTimeout(%q) = %v, want %v", in, got, want)
		}
	}
}

// launchForeignChrome starts a Chrome that plays the user's browser: one tab
// with a page of its own. It returns the browser URL and that tab's target.
func launchForeignChrome(t *testing.T) (string, *rod.Browser, proto.TargetTargetID) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // isolate the session state files
	l := NewLauncher(LauncherOpts{Headless: true, UserDataDir: t.TempDir()})
	ws, err := l.Launch()
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Cleanup(l.Kill)
	rb := rod.New().ControlURL(ws).MustConnect()
	for _, p := range rb.MustPages() { // Rod may start Chrome with a blank tab
		p.MustClose()
	}
	user := rb.MustPage("data:text/html,<title>user</title><h1>user tab</h1>")
	user.MustWaitLoad()
	return ws, rb, user.TargetID
}

// pageURLs lists the open tabs. Closing through /json/close is asynchronous,
// so it skips tabs that vanish while being read and waits (bounded) until the
// count matches want when want >= 0.
func pageURLs(t *testing.T, rb *rod.Browser, want int) map[proto.TargetTargetID]string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out := map[proto.TargetTargetID]string{}
		pages, err := rb.Pages()
		if err != nil {
			t.Fatalf("list pages: %v", err)
		}
		for _, p := range pages {
			if info, err := p.Info(); err == nil {
				out[p.TargetID] = info.URL
			}
		}
		if want < 0 || len(out) == want || time.Now().After(deadline) {
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestOwnTabLeavesUserTabsAndIsClosedOnCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	ws, rb, userTab := launchForeignChrome(t)

	// Two separate CLI invocations: the second reuses the first one's tab.
	var owned proto.TargetTargetID
	for i := 0; i < 2; i++ {
		b, err := NewBrowserWith(BrowserOpts{ConnectURL: ws, Headless: true, TimeoutSec: 15, OwnTab: true})
		if err != nil {
			t.Fatalf("connect %d: %v", i, err)
		}
		p, err := b.Page()
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		if p.TargetID == userTab {
			t.Fatalf("invocation %d drove the user's tab", i)
		}
		if i == 0 {
			owned = p.TargetID
			p.MustNavigate("data:text/html,<h1>agent</h1>")
		} else if p.TargetID != owned {
			t.Fatalf("invocation 2 used %s, want the owned tab %s", p.TargetID, owned)
		}
		b.Close()
	}

	urls := pageURLs(t, rb, 2)
	if urls[userTab] != "data:text/html,<title>user</title><h1>user tab</h1>" {
		t.Fatalf("user tab changed: %q", urls[userTab])
	}
	if _, ok := urls[owned]; !ok {
		t.Fatal("owned tab must survive between invocations")
	}

	closed, err := CloseOwnedTabs(ws, 10*time.Second)
	if err != nil || closed != 1 {
		t.Fatalf("CloseOwnedTabs = %d, %v; want 1", closed, err)
	}
	urls = pageURLs(t, rb, 1)
	if _, ok := urls[owned]; ok {
		t.Fatal("owned tab still open after cleanup")
	}
	if _, ok := urls[userTab]; !ok || len(urls) != 1 {
		t.Fatalf("after cleanup want only the user tab, got %v", urls)
	}
}

func TestBlankPagesResetsAnOwnedChrome(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	ws, rb, _ := launchForeignChrome(t)
	rb.MustPage("data:text/html,<h1>second</h1>")
	u, _ := url.Parse(ws)
	port, _ := strconv.Atoi(u.Port())

	cleared, err := BlankPages(port, 5*time.Second)
	if err != nil || cleared != 2 {
		t.Fatalf("BlankPages = %d, %v; want 2", cleared, err)
	}
	urls := pageURLs(t, rb, 1)
	if len(urls) != 1 {
		t.Fatalf("want one blank tab, got %v", urls)
	}
	for _, v := range urls {
		if v != "about:blank" {
			t.Fatalf("remaining tab on %q", v)
		}
	}
	// Already clean: nothing to do, and the browser stays up.
	if cleared, err := BlankPages(port, 5*time.Second); err != nil || cleared != 0 {
		t.Fatalf("second BlankPages = %d, %v; want 0", cleared, err)
	}
}
