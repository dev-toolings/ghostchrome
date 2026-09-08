package antibot

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/core/coretest"
	"github.com/dev-toolings/ghostchrome/internal/core/engine"
)

// TestAntiBotPatternsCurated guards against accidental list rot — patterns
// must be unique, non-empty, and use the *://host/path glob form CDP expects.
func TestAntiBotPatternsCurated(t *testing.T) {
	if len(AntiBotPatterns) == 0 {
		t.Fatal("AntiBotPatterns is empty — at least DataDome must be blocked")
	}
	seen := map[string]bool{}
	for _, p := range AntiBotPatterns {
		if p == "" {
			t.Error("empty pattern in AntiBotPatterns")
		}
		if !strings.HasPrefix(p, "*://") && !strings.HasPrefix(p, "http") {
			t.Errorf("pattern %q must start with scheme glob (*://) or absolute URL", p)
		}
		if !strings.Contains(p, "*") {
			t.Errorf("pattern %q has no wildcard — too narrow, may miss subdomains/paths", p)
		}
		if seen[p] {
			t.Errorf("duplicate pattern %q", p)
		}
		seen[p] = true
	}
	// Smoke check: at least one DataDome and one PerimeterX pattern present.
	hasDD, hasPX := false, false
	for _, p := range AntiBotPatterns {
		if strings.Contains(p, "datadome") || strings.Contains(p, "datado.me") {
			hasDD = true
		}
		if strings.Contains(p, "perimeterx") {
			hasPX = true
		}
	}
	if !hasDD {
		t.Error("expected at least one DataDome pattern")
	}
	if !hasPX {
		t.Error("expected at least one PerimeterX pattern")
	}
}

// TestStartAntiBotBlockerBlocksMatchingScript spins up a local httptest server
// that serves an HTML page loading a script from itself. The script flips a
// window flag. With a blocker matching the script URL, the flag must remain
// undefined; without blocker, it must be set.
func TestStartAntiBotBlockerBlocksMatchingScript(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><head>
<script src="/fake-datadome.js"></script>
</head><body><h1>antibot test</h1></body></html>`)
	})
	mux.HandleFunc("/fake-datadome.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, `window.__DD_LOADED = true;`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	// Build a glob that matches the test server's fake script. Format mirrors
	// AntiBotPatterns ("*://host/path*") so we exercise the same code path.
	pattern := "*://" + strings.TrimPrefix(server.URL, "http://") + "/fake-datadome.js*"

	t.Run("without blocker the script runs", func(t *testing.T) {
		_, page := coretest.NewIsolatedPage(t)
		if _, err := engine.Navigate(page, server.URL+"/", "load"); err != nil {
			t.Fatalf("navigate: %v", err)
		}
		got, err := engine.EvalJS(page, `String(window.__DD_LOADED)`, "", nil)
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if got != "true" {
			t.Fatalf("baseline: expected window.__DD_LOADED=true (script ran), got %q", got)
		}
	})

	t.Run("with blocker the script is rejected", func(t *testing.T) {
		b, page := coretest.NewIsolatedPage(t)
		sess, err := StartAntiBotBlocker(b.RodBrowser(), pattern)
		if err != nil {
			t.Fatalf("StartAntiBotBlocker: %v", err)
		}
		defer func() { _ = sess.Stop() }()

		if _, err := engine.Navigate(page, server.URL+"/", "load"); err != nil {
			t.Fatalf("navigate: %v", err)
		}
		got, err := engine.EvalJS(page, `String(window.__DD_LOADED)`, "", nil)
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if got != "undefined" {
			t.Fatalf("expected window.__DD_LOADED=undefined (script blocked), got %q", got)
		}

		blocked, _, _ := sess.Stats().Snapshot()
		if blocked < 1 {
			t.Fatalf("expected at least 1 blocked request, got %d", blocked)
		}
	})
}

// TestStartAntiBotBlockerLetsHTMLThrough confirms the blocker only kills the
// targeted script — the page itself must still render.
func TestStartAntiBotBlockerLetsHTMLThrough(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><head>
<script src="/blocked.js"></script>
</head><body><h1 id="loaded">page rendered</h1></body></html>`)
	})
	mux.HandleFunc("/blocked.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, `window.__SHOULD_NOT_RUN = true;`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	pattern := "*://" + strings.TrimPrefix(server.URL, "http://") + "/blocked.js*"

	b, page := coretest.NewIsolatedPage(t)
	sess, err := StartAntiBotBlocker(b.RodBrowser(), pattern)
	if err != nil {
		t.Fatalf("StartAntiBotBlocker: %v", err)
	}
	defer func() { _ = sess.Stop() }()

	if _, err := engine.Navigate(page, server.URL+"/", "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	heading, err := engine.EvalJS(page, `document.getElementById('loaded')?.textContent`, "", nil)
	if err != nil {
		t.Fatalf("eval heading: %v", err)
	}
	if heading != "page rendered" {
		t.Fatalf("expected HTML to render despite blocked script, got %q", heading)
	}

	blocked, _, _ := sess.Stats().Snapshot()
	if blocked < 1 {
		t.Fatalf("expected blocker to register at least 1 hit, got %d", blocked)
	}
}
