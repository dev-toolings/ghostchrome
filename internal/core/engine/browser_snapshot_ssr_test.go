package engine

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// TestStripSSRForCacheClearsPayloadsWithoutMutatingOriginal is a pure,
// network-free regression test for the SSR/cache leak fix: SaveSnapshot must
// never persist SSRPayloads (see stripSSRForCache), and the caller's own
// ExtractionResult — returned as the immediate command response — must stay
// untouched.
func TestStripSSRForCacheClearsPayloadsWithoutMutatingOriginal(t *testing.T) {
	original := &ExtractionResult{
		Refs:        map[string]ExtractedNode{},
		SSRPayloads: []SSRPayload{{Source: SourceNextData, Data: []byte(`{"token":"secret"}`), IsJSON: true}},
	}

	stripped := stripSSRForCache(original)

	if len(original.SSRPayloads) != 1 {
		t.Fatalf("caller's result must be left untouched, got %+v", original.SSRPayloads)
	}
	if stripped == original {
		t.Fatal("expected a distinct copy when SSRPayloads must be stripped")
	}
	if len(stripped.SSRPayloads) != 0 {
		t.Fatalf("expected stripped copy to carry no SSR payloads, got %+v", stripped.SSRPayloads)
	}
}

// TestStripSSRForCacheReturnsSamePointerWhenNothingToStrip avoids an
// unnecessary allocation on the hot (non-SSR) path.
func TestStripSSRForCacheReturnsSamePointerWhenNothingToStrip(t *testing.T) {
	original := &ExtractionResult{Nodes: []ExtractedNode{{Role: "link"}}}
	if got := stripSSRForCache(original); got != original {
		t.Fatalf("expected the same pointer back when there is nothing to strip")
	}
	if got := stripSSRForCache(nil); got != nil {
		t.Fatalf("expected nil to pass through unchanged, got %+v", got)
	}
}

// TestSaveSnapshotStripsSSRPayloadsFromCache is an end-to-end regression test
// (real headless Chrome, local HTTP server only — no live network) for the
// SSR/cache leak: SaveSnapshot must never persist SSRPayloads into the
// on-disk cache that CachedExtract later serves to non-SSR callers.
func TestSaveSnapshotStripsSSRPayloadsFromCache(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body><button>hi</button></body></html>`))
	}))
	defer server.Close()

	b, cleanup := testBrowser(t)
	defer cleanup()

	page, err := b.Page()
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if _, err := Navigate(page, server.URL, "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// Simulate the recovery path: an extraction that opted into SSR and got
	// payloads back (a real DataDome/SSR page would populate these).
	withSSR := &ExtractionResult{
		Refs:        map[string]ExtractedNode{},
		SSRPayloads: []SSRPayload{{Source: SourceNextData, Data: []byte(`{"token":"secret"}`), IsJSON: true}},
	}
	if err := b.SaveSnapshot(page, withSSR); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	if len(withSSR.SSRPayloads) != 1 {
		t.Fatalf("caller's result must remain untouched after SaveSnapshot, got %+v", withSSR.SSRPayloads)
	}

	cached := b.CachedExtract(page)
	if cached == nil {
		t.Fatal("expected a cached extraction result")
	}
	if len(cached.SSRPayloads) != 0 {
		t.Fatalf("expected cached extraction to never leak SSR payloads to a non-SSR caller, got %+v", cached.SSRPayloads)
	}
}

func TestInvalidateCachedExtractClearsURLCache(t *testing.T) {
	b := &Browser{
		connected: true,
		statePath: filepath.Join(t.TempDir(), "state.json"),
		state: &sessionState{
			Snapshots: map[string]PageSnapshot{
				"target-1": {
					TargetID:         "target-1",
					URL:              "https://example.com/",
					CachedExtraction: &ExtractionResult{Nodes: []ExtractedNode{{Role: "button"}}},
				},
			},
		},
	}
	page := &rod.Page{}
	page.TargetID = proto.TargetTargetID("target-1")
	snap := b.state.Snapshots["target-1"]
	if snap.CachedExtraction == nil {
		t.Fatal("expected cached extraction before invalidate")
	}
	if err := b.InvalidateCachedExtract(page); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if b.state.Snapshots["target-1"].CachedExtraction != nil {
		t.Fatal("expected cached extraction to be cleared")
	}
}
