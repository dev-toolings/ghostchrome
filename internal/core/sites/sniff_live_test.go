//go:build live
// +build live

package sites_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/sites"
)

// TestSniffCapcar is a live network test gated by `-tags live`.
// Verifies the CDP getRequestPostData fallback by checking that a real
// XHR with a body ~1KiB (Algolia multi-query) is captured non-empty.
func TestSniffCapcar(t *testing.T) {
	br, err := engine.NewBrowser("", true, 30)
	if err != nil {
		t.Fatalf("browser: %v", err)
	}
	defer br.Close()
	page, err := br.Page()
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	caps, err := sites.Sniff(context.Background(), page,
		"https://www.capcar.fr/voiture-occasion?brand=Peugeot",
		sites.SniffOptions{Duration: 20 * time.Second})
	if err != nil {
		t.Fatalf("sniff: %v", err)
	}
	if len(caps) == 0 {
		t.Fatalf("no candidates captured")
	}
	var algoliaWithBody int
	for _, c := range caps {
		if strings.Contains(c.URL, "algolia") && c.Method == "POST" && len(c.RequestBody) > 0 {
			algoliaWithBody++
			if !strings.Contains(c.RequestBody, "indexName") {
				t.Errorf("algolia body missing indexName: %q", c.RequestBody[:min(120, len(c.RequestBody))])
			}
		}
	}
	if algoliaWithBody == 0 {
		t.Fatalf("no algolia POST with non-empty RequestBody (regression: getRequestPostData fallback broken)")
	}
	t.Logf("captured %d algolia POSTs with body", algoliaWithBody)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
