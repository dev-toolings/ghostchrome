package cli

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/go-rod/rod/lib/launcher"
)

// TestSnapshotPageBypassesCacheWhenSSRRequested is a regression test (real
// headless Chrome, local HTTP server only — no live network) for the
// SSR/cache leak: snapshots must refresh at the requested level, including
// SSR opt-in, and must observe DOM changes even when the URL stays unchanged.
func TestSnapshotPageBypassesCacheWhenSSRRequested(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	t.Setenv("HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body><button>hi</button></body></html>`))
	}))
	defer server.Close()

	l := launcher.New().Headless(true).Leakless(false).NoSandbox(true).
		UserDataDir(filepath.Join(t.TempDir(), "chrome-profile"))
	controlURL, err := l.Launch()
	if err != nil {
		engine.CleanupFailedLauncher(l, true)
		t.Fatalf("launch browser: %v", err)
	}
	defer func() {
		l.Kill()
		l.Cleanup()
	}()

	b, err := engine.NewBrowser(controlURL, true, 10)
	if err != nil {
		t.Fatalf("new browser: %v", err)
	}
	defer b.Close()

	page, err := b.Page()
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if _, err := engine.Navigate(page, server.URL, "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	first := snapshotPage(b, page, engine.LevelSkeleton)
	if first == nil {
		t.Fatal("expected a non-nil extraction result")
	}

	if _, err := page.Eval(`() => { document.body.innerHTML += '<p>fresh paragraph marker</p>'; }`); err != nil {
		t.Fatal(err)
	}
	second := snapshotPage(b, page, engine.LevelContent)
	if !strings.Contains(engine.FormatText(second), "fresh paragraph marker") {
		t.Fatal("content snapshot reused stale skeleton cache")
	}

	third := snapshotPage(b, page, engine.LevelSkeleton, true)
	if third == second {
		t.Fatal("expected includeSSR=true to bypass the cache and recompute a fresh result")
	}
}
