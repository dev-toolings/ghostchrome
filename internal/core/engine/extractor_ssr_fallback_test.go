package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestExtractSSRFallback_NextData exercises the fallback wiring in
// ExtractWithTimeout (not just the ExtractSSRPayloads parser, already
// covered by ssr_extract_test.go): when the accessibility tree comes back
// empty, the extraction must fall back to the page's raw HTML.
//
// The page below is a SYNTHETIC fixture styled after a Next.js Pages-Router
// SSR payload (__NEXT_DATA__) — it is NOT a captured lacentrale response.
// The live capture (engine/testdata/lacentrale_listing.html) never reached a
// hydrated state — DataDome served an interactive CAPTCHA instead — so this
// test validates the new glue path generically rather than against a real
// hydrated lacentrale page.
func TestExtractSSRFallback_NextData(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><body>
<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"data":{"content":[{"id":1,"testid":"vehicleCardV2"},{"id":2,"testid":"vehicleCardV2"}]}}}}</script>
</body></html>`)
	}))
	defer server.Close()

	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL+"/", "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	result, err := Extract(page, LevelContent, "", true)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(result.Nodes) != 0 {
		t.Fatalf("expected an empty a11y tree for this content-less page, got %d nodes", len(result.Nodes))
	}
	if len(result.SSRPayloads) != 1 || result.SSRPayloads[0].Source != SourceNextData {
		t.Fatalf("expected 1 next-data SSR payload fallback, got %+v", result.SSRPayloads)
	}

	var parsed struct {
		Props struct {
			PageProps struct {
				Data struct {
					Content []struct {
						ID     int    `json:"id"`
						TestID string `json:"testid"`
					} `json:"content"`
				} `json:"data"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal(result.SSRPayloads[0].Data, &parsed); err != nil {
		t.Fatalf("unmarshal next-data payload: %v", err)
	}
	cards := parsed.Props.PageProps.Data.Content
	if len(cards) != 2 || cards[0].TestID != "vehicleCardV2" {
		t.Fatalf("expected 2 vehicleCardV2 cards in the fallback payload, got %+v", cards)
	}
}

// TestExtractSSRFallback_RSC mirrors TestExtractSSRFallback_NextData for the
// Next.js App Router shape (self.__next_f.push RSC chunks) rather than
// __NEXT_DATA__ — leboncoin has already migrated to this shape, and
// lacentrale may follow, so the fallback wiring must not be __NEXT_DATA__-only.
func TestExtractSSRFallback_RSC(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><body>
<script>self.__next_f.push([1, "3:[{\"id\":1,\"testid\":\"vehicleCardV2\"}]\n"])</script>
</body></html>`)
	}))
	defer server.Close()

	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL+"/", "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	result, err := Extract(page, LevelContent, "", true)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(result.Nodes) != 0 {
		t.Fatalf("expected an empty a11y tree for this content-less page, got %d nodes", len(result.Nodes))
	}
	if len(result.SSRPayloads) != 1 || result.SSRPayloads[0].Source != SourceRSC {
		t.Fatalf("expected 1 RSC SSR payload fallback, got %+v", result.SSRPayloads)
	}
	if !result.SSRPayloads[0].IsJSON {
		t.Fatalf("expected the RSC chunk to be recognised as JSON, got %+v", result.SSRPayloads[0])
	}
}

// TestExtractSSRFallback_OptOutByDefault asserts that SSRPayloads is NOT
// populated unless the caller explicitly opts in (includeSSR=true). These
// payloads (__NEXT_DATA__, __APOLLO_STATE__, RSC chunks, ...) can carry
// tokens/PII on an authenticated page, so callers must not get them for free.
func TestExtractSSRFallback_OptOutByDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><body>
<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"data":{"content":[{"id":1,"testid":"vehicleCardV2"}]}}}}</script>
</body></html>`)
	}))
	defer server.Close()

	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL+"/", "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	result, err := Extract(page, LevelContent, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(result.Nodes) != 0 {
		t.Fatalf("expected an empty a11y tree for this content-less page, got %d nodes", len(result.Nodes))
	}
	if len(result.SSRPayloads) != 0 {
		t.Fatalf("expected no SSR payload fallback without opt-in, got %+v", result.SSRPayloads)
	}
}
