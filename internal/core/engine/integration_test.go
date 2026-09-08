package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

func TestRefsUseAXTargetsAcrossActions(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	html := `<!doctype html>
<html>
  <body>
    <button id="hidden-button" style="display:none" onclick="window.clicked='hidden'">Hidden Button</button>
    <button id="visible-button" onclick="window.clicked='visible'">Visible Button</button>
    <input id="hidden-input" aria-label="Hidden Input" style="display:none" value="">
    <input id="visible-input" aria-label="Visible Input" value="">
  </body>
</html>`

	if _, err := Navigate(page, dataURL(html), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if got := result.Refs["@1"].Name; got != "Visible Button" {
		t.Fatalf("expected @1 to resolve to visible button, got %q", got)
	}
	if got := result.Refs["@2"].Name; got != "Visible Input" {
		t.Fatalf("expected @2 to resolve to visible input, got %q", got)
	}

	snapshot, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	if err := ClickRef(page, "@1", snapshot); err != nil {
		t.Fatalf("click @1: %v", err)
	}
	clicked, err := EvalJS(page, `window.clicked`, "", snapshot)
	if err != nil {
		t.Fatalf("eval clicked: %v", err)
	}
	if clicked != "visible" {
		t.Fatalf("expected visible button click, got %q", clicked)
	}

	if err := TypeRef(page, "@2", "hello", snapshot); err != nil {
		t.Fatalf("type @2: %v", err)
	}
	value, err := EvalJS(page, `document.getElementById("visible-input").value`, "", snapshot)
	if err != nil {
		t.Fatalf("eval visible input: %v", err)
	}
	if value != "hello" {
		t.Fatalf("expected visible input value to be updated, got %q", value)
	}
	hiddenValue, err := EvalJS(page, `document.getElementById("hidden-input").value`, "", snapshot)
	if err != nil {
		t.Fatalf("eval hidden input: %v", err)
	}
	if hiddenValue != "" {
		t.Fatalf("expected hidden input to remain untouched, got %q", hiddenValue)
	}

	buttonText, err := EvalJS(page, `this.textContent.trim()`, "@1", snapshot)
	if err != nil {
		t.Fatalf("eval @1: %v", err)
	}
	if buttonText != "Visible Button" {
		t.Fatalf("expected element eval to bind to visible button, got %q", buttonText)
	}

	screenshot, err := TakeScreenshot(page, false, "@1", 0, snapshot)
	if err != nil {
		t.Fatalf("screenshot @1: %v", err)
	}
	if len(screenshot) == 0 {
		t.Fatal("expected non-empty screenshot bytes")
	}

}

func TestNavigateTracksFinalRedirectStatusWithoutDelay(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/mid", http.StatusFound)
		case "/mid":
			http.Redirect(w, r, "/final", http.StatusSeeOther)
		case "/final":
			fmt.Fprint(w, "<!doctype html><title>Final</title><h1>Final</h1>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, page := newIsolatedPage(t)
	start := time.Now()
	info, err := Navigate(page, server.URL+"/start", "load")
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	elapsed := time.Since(start)

	if info.Status != http.StatusOK {
		t.Fatalf("expected final status 200, got %d", info.Status)
	}
	if info.URL != server.URL+"/final" {
		t.Fatalf("expected final url %q, got %q", server.URL+"/final", info.URL)
	}
	if elapsed >= 4*time.Second {
		t.Fatalf("expected redirect navigation to avoid fixed delay, took %v", elapsed)
	}
}

func TestDefaultPageProfileAppliesHeadersLanguageAndViewport(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	var (
		mu       sync.Mutex
		navHdrs  http.Header
		navSeen  bool
		document = "/"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the navigation request is recorded. Chrome asks for
		// /favicon.ico on its own, and a subresource carries no
		// Upgrade-Insecure-Requests — so whenever that second request landed
		// before the assertions ran, it overwrote the very header this test
		// exists to check and the run failed with `got ""`. Which of the two
		// arrived first was a race, which is why it failed roughly one run in
		// two rather than always.
		if r.URL.Path != document {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		mu.Lock()
		if !navSeen {
			navHdrs, navSeen = r.Header.Clone(), true
		}
		mu.Unlock()
		fmt.Fprint(w, "<!doctype html><title>profile</title><h1>profile</h1>")
	}))
	defer server.Close()

	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// The handler runs on the server's goroutine: -race is right to object to
	// reading these without the lock, whatever the timing happens to be.
	mu.Lock()
	seen, hdrs := navSeen, navHdrs
	mu.Unlock()

	if !seen {
		t.Fatal("le serveur n'a jamais vu la requête de navigation")
	}
	acceptLanguage := hdrs.Get("Accept-Language")
	upgradeInsecure := hdrs.Get("Upgrade-Insecure-Requests")
	dntHeader := hdrs.Get("Dnt")

	if !strings.Contains(acceptLanguage, "fr-FR") {
		t.Fatalf("expected Accept-Language to contain fr-FR, got %q", acceptLanguage)
	}
	if upgradeInsecure != "1" {
		t.Fatalf("expected Upgrade-Insecure-Requests=1, got %q", upgradeInsecure)
	}
	if dntHeader != "1" {
		t.Fatalf("expected DNT=1, got %q", dntHeader)
	}

	ua, err := EvalJS(page, `navigator.userAgent`, "", nil)
	if err != nil {
		t.Fatalf("eval userAgent: %v", err)
	}
	if !strings.Contains(ua, "Chrome/135.0.0.0") {
		t.Fatalf("expected overridden Chrome UA, got %q", ua)
	}

	lang, err := EvalJS(page, `navigator.language`, "", nil)
	if err != nil {
		t.Fatalf("eval navigator.language: %v", err)
	}
	if !strings.HasPrefix(lang, "fr") {
		t.Fatalf("expected French navigator.language, got %q", lang)
	}

	width, err := EvalJS(page, `window.innerWidth`, "", nil)
	if err != nil {
		t.Fatalf("eval innerWidth: %v", err)
	}
	height, err := EvalJS(page, `window.innerHeight`, "", nil)
	if err != nil {
		t.Fatalf("eval innerHeight: %v", err)
	}
	if width != "1440" {
		t.Fatalf("expected default viewport width 1440, got %q", width)
	}
	if height != "900" {
		t.Fatalf("expected default viewport height 900, got %q", height)
	}
}

func TestConnectedPageSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		title := strings.TrimPrefix(r.URL.Path, "/")
		if title == "" {
			title = "root"
		}
		fmt.Fprintf(w, "<!doctype html><title>%s</title><h1>%s</h1>", title, title)
	}))
	defer server.Close()

	connectURL, raw := newConnectedBrowser(t)
	firstURL := server.URL + "/one"
	secondURL := server.URL + "/two"

	pages, err := raw.Pages()
	if err != nil {
		t.Fatalf("list initial pages: %v", err)
	}
	if len(pages) == 0 {
		page, err := raw.Page(proto.TargetCreateTarget{})
		if err != nil {
			t.Fatalf("create initial page: %v", err)
		}
		pages = rod.Pages{page}
	}
	if err := pages[0].Navigate(firstURL); err != nil {
		t.Fatalf("seed first page: %v", err)
	}
	if err := pages[0].WaitLoad(); err != nil {
		t.Fatalf("wait first page: %v", err)
	}

	t.Run("single non-blank page without tab selects existing page", func(t *testing.T) {
		b, err := NewBrowser(connectURL, true, 10)
		if err != nil {
			t.Fatalf("connect browser: %v", err)
		}
		page, err := b.Page()
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		info, err := page.Info()
		if err != nil {
			t.Fatalf("page info: %v", err)
		}
		if info.URL != firstURL {
			t.Fatalf("expected selected page %q, got %q", firstURL, info.URL)
		}
	})

	secondPage, err := raw.Page(proto.TargetCreateTarget{})
	if err != nil {
		t.Fatalf("create second page: %v", err)
	}
	if err := secondPage.Navigate(secondURL); err != nil {
		t.Fatalf("seed second page: %v", err)
	}
	if err := secondPage.WaitLoad(); err != nil {
		t.Fatalf("wait second page: %v", err)
	}

	t.Run("multiple non-blank pages without tab fail deterministically", func(t *testing.T) {
		b, err := NewBrowser(connectURL, true, 10)
		if err != nil {
			t.Fatalf("connect browser: %v", err)
		}
		if _, err := b.Page(); err == nil {
			t.Fatal("expected ambiguity error when multiple non-blank pages exist")
		} else if !strings.Contains(err.Error(), "multiple connected tabs available") {
			t.Fatalf("expected ambiguity error, got %v", err)
		}
	})

	t.Run("explicit tab selects requested page", func(t *testing.T) {
		tabs, err := ListTabs(raw, "")
		if err != nil {
			t.Fatalf("list tabs: %v", err)
		}
		targetIndex := -1
		for _, tab := range tabs {
			if tab.URL == secondURL {
				targetIndex = tab.Index
				break
			}
		}
		if targetIndex < 0 {
			t.Fatalf("expected to find tab for %q", secondURL)
		}

		b, err := NewBrowserWith(BrowserOpts{
			ConnectURL:     connectURL,
			Headless:       true,
			TimeoutSec:     10,
			TargetTabIndex: &targetIndex,
		})
		if err != nil {
			t.Fatalf("connect browser: %v", err)
		}
		page, err := b.Page()
		if err != nil {
			t.Fatalf("page with tab: %v", err)
		}
		info, err := page.Info()
		if err != nil {
			t.Fatalf("page info: %v", err)
		}
		if info.URL != secondURL {
			t.Fatalf("expected explicit tab to resolve %q, got %q", secondURL, info.URL)
		}
	})
}

func newIsolatedPage(t *testing.T) (*Browser, *rod.Page) {
	t.Helper()

	b, err := NewBrowser("", true, 10)
	if err != nil {
		t.Fatalf("new browser: %v", err)
	}
	t.Cleanup(func() {
		b.Close()
	})

	page, err := b.Page()
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	return b, page
}

func newConnectedBrowser(t *testing.T) (string, *rod.Browser) {
	t.Helper()

	l := launcher.New().Headless(true).NoSandbox(needsNoSandbox()).
		UserDataDir(filepath.Join(t.TempDir(), "chrome-profile"))
	controlURL, err := l.Launch()
	if err != nil {
		CleanupFailedLauncher(l, true)
		t.Fatalf("launch connected browser: %v", err)
	}

	raw := rod.New().ControlURL(controlURL).Timeout(10 * time.Second)
	t.Cleanup(func() {
		_ = raw.Close()
		l.Kill()
		l.Cleanup()
	})
	if err := raw.Connect(); err != nil {
		t.Fatalf("connect raw browser: %v", err)
	}

	return controlURL, raw
}

func dataURL(html string) string {
	return "data:text/html," + url.PathEscape(html)
}
