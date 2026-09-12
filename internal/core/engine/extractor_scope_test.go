package engine

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
)

// scopeFixture mirrors the shape that broke in production: a roleless wrapper
// (a Next.js root, a Tailwind div) holding the page's real content, plus a
// console error that must survive whatever happens to the selector.
const scopeFixture = `<!doctype html>
<html><head><title>Scope fixture</title></head>
<body>
  <button id="outside">Outside button</button>
  <div id="app">
    <h2>Scoped heading</h2>
    <button id="inside">Scoped button</button>
    <a href="/somewhere">Scoped link</a>
  </div>
  <div id="ghost" aria-hidden="true"><button>Ghost button</button></div>
  <script>console.error("boom from the fixture")</script>
</body></html>`

func scopeFixturePage(t *testing.T) *rod.Page {
	t.Helper()
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(scopeFixture))
	}))
	t.Cleanup(server.Close)

	b, cleanup := testBrowser(t)
	t.Cleanup(cleanup)

	page, err := b.Page()
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if _, err := Navigate(page, server.URL, "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	return page
}

func collectNames(nodes []ExtractedNode, out *[]string) {
	for _, n := range nodes {
		if n.Name != "" {
			*out = append(*out, n.Name)
		}
		collectNames(n.Children, out)
	}
}

func extractedNames(result *ExtractionResult) []string {
	var names []string
	collectNames(result.Nodes, &names)
	return names
}

func hasName(result *ExtractionResult, want string) bool {
	for _, name := range extractedNames(result) {
		if strings.Contains(name, want) {
			return true
		}
	}
	return false
}

// A selector that matches nothing used to inherit the page deadline and retry
// document.querySelector for minutes. It must now come back at once, with a
// warning and the full page rather than an error.
func TestExtractUnknownSelectorFailsFast(t *testing.T) {
	page := scopeFixturePage(t)

	start := time.Now()
	result, err := Extract(page, LevelContent, "#no-such-element", false)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("a bad selector must not fail the extraction: %v", err)
	}
	t.Logf("unknown selector resolved in %s", elapsed)
	if elapsed >= time.Second {
		t.Fatalf("selector resolution took %s, expected under 1s", elapsed)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], ErrSelectorNoMatch.Error()) {
		t.Fatalf("expected a no-match warning, got %#v", result.Warnings)
	}
	if !hasName(result, "Outside button") {
		t.Fatalf("expected the full page back after a bad selector, got %v", extractedNames(result))
	}
}

// The wrapper carries no role and no aria-label, so it is pruned from the
// accessibility tree. Scoping must still return the named nodes of its DOM
// subtree, and only those.
func TestExtractSelectorScopesRolelessWrapper(t *testing.T) {
	page := scopeFixturePage(t)

	result, err := Extract(page, LevelContent, "#app", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warning for a selector that resolves, got %#v", result.Warnings)
	}
	for _, want := range []string{"Scoped heading", "Scoped button", "Scoped link"} {
		if !hasName(result, want) {
			t.Fatalf("expected %q in the scoped subtree, got %v", want, extractedNames(result))
		}
	}
	if hasName(result, "Outside button") {
		t.Fatalf("scoped extraction leaked a node from outside the subtree: %v", extractedNames(result))
	}
}

// An aria-hidden wrapper resolves to a scope that holds nothing extractable.
// An empty answer would be worse than useless, so the extraction degrades to
// the full page and says why.
func TestExtractEmptyScopeFallsBackToFullPage(t *testing.T) {
	page := scopeFixturePage(t)

	result, err := Extract(page, LevelContent, "#ghost", false)
	if err != nil {
		t.Fatalf("an empty scope must not fail the extraction: %v", err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], ErrSelectorNoAXNode.Error()) {
		t.Fatalf("expected an empty-scope warning, got %#v", result.Warnings)
	}
	if !hasName(result, "Outside button") {
		t.Fatalf("expected the full page back after an empty scope, got %v", extractedNames(result))
	}
}

// The status, the console errors and the network of a page have nothing to do
// with its selector: a snapshot must keep reporting them when scoping fails.
// This composes the same pieces the MCP snapshot tool does.
func TestSnapshotKeepsStatusAndConsoleOnBadSelector(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(scopeFixture))
	}))
	defer server.Close()

	b, cleanup := testBrowser(t)
	defer cleanup()

	page, err := b.Page()
	if err != nil {
		t.Fatalf("page: %v", err)
	}

	collector := NewErrorCollector(page)
	defer collector.Close()

	info, err := Navigate(page, server.URL, "load")
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}

	result, err := Extract(page, LevelContent, "main, body > div:first-child > nope", false)
	if err != nil {
		t.Fatalf("a bad selector must not fail the snapshot: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected the failed scoping to be reported as a warning")
	}
	if info.Status != 200 {
		t.Fatalf("expected the HTTP status to survive a bad selector, got %d", info.Status)
	}

	var console []string
	for _, entry := range collector.Errors() {
		console = append(console, entry.Message)
	}
	if !strings.Contains(strings.Join(console, "\n"), "boom from the fixture") {
		t.Fatalf("expected the console error to survive a bad selector, got %v", console)
	}
}
