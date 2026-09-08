// Package coretest provides the browser scaffolding shared by the tests of
// the packages extracted out of engine. Package engine keeps its own copies
// of these helpers: it may not import a package that imports it back.
package coretest

import (
	"net/url"
	"testing"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
)

// NewIsolatedPage boots a throwaway headless browser and returns a blank page
// on it. The browser is closed when the test ends.
func NewIsolatedPage(t *testing.T) (*engine.Browser, *rod.Page) {
	t.Helper()

	b, err := engine.NewBrowser("", true, 10)
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

// DataURL wraps an HTML fragment into a navigable data: URL.
func DataURL(html string) string {
	return "data:text/html," + url.PathEscape(html)
}
