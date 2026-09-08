package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCaptureMutationWaitsForXHR(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fragment":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<button id="new-action">Loaded action</button>`))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><div id="target"><button id="load">Load</button></div><script>
				document.getElementById('load').addEventListener('click', () => {
					setTimeout(() => fetch('/fragment').then((response) => response.text()).then((html) => {
						document.getElementById('target').innerHTML = html;
					}), 20);
				});
			</script>`))
		}
	}))
	t.Cleanup(server.Close)

	b, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	before, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract before click: %v", err)
	}
	prev, err := BuildSnapshot(page, before)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if err := ClickRef(page, "@1", prev); err != nil {
		t.Fatalf("click: %v", err)
	}

	_, result, err := CaptureMutation(b, page, prev)
	if err != nil {
		t.Fatalf("capture mutation: %v", err)
	}
	if result == nil {
		t.Fatal("capture mutation returned no extraction")
	}
	for _, node := range result.Refs {
		if node.Name == "Loaded action" {
			return
		}
	}
	t.Fatal("new interactive ref missing from extraction")
}

func TestCaptureMutationFastOnStatic(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	b, page := newIsolatedPage(t)
	if _, err := Navigate(page, dataURL(`<!doctype html><button>Static</button>`), "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	before, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	prev, err := BuildSnapshot(page, before)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	start := time.Now()
	if _, _, err := CaptureMutation(b, page, prev); err != nil {
		t.Fatalf("capture mutation: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 200*time.Millisecond {
		t.Fatalf("static mutation capture took %s, want under 200ms", elapsed)
	}
}
