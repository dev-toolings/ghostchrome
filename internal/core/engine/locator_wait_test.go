package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestWaitForLocatorAttached checks that WaitForLocator returns quickly when
// the element is already in the DOM.
func TestWaitForLocatorAttached(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	html := `<!doctype html><html><body>
		<button id="btn">Click me</button>
	</body></html>`

	if _, err := Navigate(page, dataURL(html), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	el, err := WaitForLocator(page, Locator{Role: "button", Name: "Click me"}, StateAttached, 3*time.Second)
	if err != nil {
		t.Fatalf("WaitForLocator attached: %v", err)
	}
	if el == nil {
		t.Fatal("expected non-nil element")
	}
}

// TestWaitForLocatorVisible waits for a hidden element to become visible via JS.
func TestWaitForLocatorVisible(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	html := `<!doctype html><html><body>
		<button id="btn" style="display:none">Hidden Button</button>
		<script>
			setTimeout(function(){ document.getElementById('btn').style.display=''; }, 300);
		</script>
	</body></html>`

	if _, err := Navigate(page, dataURL(html), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	el, err := WaitForLocator(page, Locator{Role: "button", Name: "Hidden Button"}, StateVisible, 3*time.Second)
	if err != nil {
		t.Fatalf("WaitForLocator visible: %v", err)
	}
	if el == nil {
		t.Fatal("expected non-nil element")
	}
}

// TestWaitForLocatorHidden waits for a visible element to disappear.
// When the element is removed from the AX tree (display:none), WaitForLocator
// with StateHidden returns (nil, nil) — nil element is the expected result.
func TestWaitForLocatorHidden(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	html := `<!doctype html><html><body>
		<button id="btn">Visible Button</button>
		<script>
			setTimeout(function(){ document.getElementById('btn').style.display='none'; }, 300);
		</script>
	</body></html>`

	if _, err := Navigate(page, dataURL(html), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// StateHidden: we expect success (nil error). The element reference may be
	// nil when the element was removed from the AX tree (display:none).
	_, err := WaitForLocator(page, Locator{Role: "button", Name: "Visible Button"}, StateHidden, 3*time.Second)
	if err != nil {
		t.Fatalf("WaitForLocator hidden: %v", err)
	}
}

// TestWaitForLocatorEnabled waits for a disabled button to become enabled.
func TestWaitForLocatorEnabled(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	html := `<!doctype html><html><body>
		<button id="btn" disabled>Submit</button>
		<script>
			setTimeout(function(){ document.getElementById('btn').disabled = false; }, 300);
		</script>
	</body></html>`

	if _, err := Navigate(page, dataURL(html), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	el, err := WaitForLocator(page, Locator{Role: "button", Name: "Submit"}, StateEnabled, 3*time.Second)
	if err != nil {
		t.Fatalf("WaitForLocator enabled: %v", err)
	}
	if el == nil {
		t.Fatal("expected non-nil element")
	}
}

// TestWaitForLocatorStable verifies that a stationary element passes the
// stable check immediately (no layout animation).
func TestWaitForLocatorStable(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	html := `<!doctype html><html><body>
		<button id="btn">Stable Button</button>
	</body></html>`

	if _, err := Navigate(page, dataURL(html), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	el, err := WaitForLocator(page, Locator{Role: "button", Name: "Stable Button"}, StateStable, 3*time.Second)
	if err != nil {
		t.Fatalf("WaitForLocator stable: %v", err)
	}
	if el == nil {
		t.Fatal("expected non-nil element")
	}
}

// TestWaitForLocatorTimeout verifies that we get an error when the element
// never appears within the deadline.
func TestWaitForLocatorTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	html := `<!doctype html><html><body><p>No button here</p></body></html>`

	if _, err := Navigate(page, dataURL(html), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	start := time.Now()
	_, err := WaitForLocator(page, Locator{Role: "button", Name: "Ghost"}, StateAttached, 400*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed < 300*time.Millisecond {
		t.Fatalf("expected to wait at least 300ms, waited %s", elapsed)
	}
}

func TestWaitForLocatorRejectsAmbiguousVisibleMatchesWithoutWaiting(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, dataURL(`<!doctype html><button>Delete</button><button>Delete all</button>`), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	started := time.Now()
	_, err := WaitForLocator(page, Locator{Role: "button", Name: "Delete"}, StateVisible, 3*time.Second)
	if err == nil || !strings.Contains(err.Error(), "matched 2 visible elements") {
		t.Fatalf("ambiguous locator error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("ambiguous locator waited %s instead of failing immediately", elapsed)
	}
}

// TestWaitForRefAttached verifies WaitForRef works for a stable snapshot.
func TestWaitForRefAttached(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	html := `<!doctype html><html><body>
		<button>My Button</button>
	</body></html>`

	if _, err := Navigate(page, dataURL(html), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	snapshot, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	el, err := WaitForRef(page, "@1", snapshot, StateAttached, 0)
	if err != nil {
		t.Fatalf("WaitForRef: %v", err)
	}
	if el == nil {
		t.Fatal("expected non-nil element")
	}
}

// TestWaitForRefVisible waits for an element that is temporarily hidden to
// become visible. The element is visible at extract time (so it gets a ref),
// then hidden, then shown again. Ref mapping is preserved throughout.
func TestWaitForRefVisible(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	// Use an HTTP server because data: URLs sometimes have script execution quirks.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><html><body>
			<button id="b">Toggle</button>
			<script>
				// Hide after 50ms, show again after 350ms.
				setTimeout(function(){ document.getElementById('b').style.display='none'; }, 50);
				setTimeout(function(){ document.getElementById('b').style.display=''; }, 350);
			</script>
		</body></html>`)
	}))
	defer server.Close()

	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// Extract immediately — button is visible, gets ref @1.
	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(result.Refs) == 0 {
		t.Fatal("expected at least one ref in skeleton")
	}
	snapshot, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	// Wait for @1 to become visible again (after the hide/show cycle).
	el, err := WaitForRef(page, "@1", snapshot, StateVisible, 3*time.Second)
	if err != nil {
		t.Fatalf("WaitForRef visible: %v", err)
	}
	if el == nil {
		t.Fatal("expected non-nil element")
	}
}

// TestWaitForRefNilSnapshot verifies ErrStaleRef is returned for nil snapshots.
func TestWaitForRefNilSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	if _, err := Navigate(page, dataURL(`<!doctype html><html><body><button>X</button></body></html>`), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	_, err := WaitForRef(page, "@1", nil, StateAttached, 0)
	if err == nil {
		t.Fatal("expected error for nil snapshot")
	}
}

func TestWaitForTargetRefAfterRerender(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	html := `<!doctype html><html><body>
<button id="go">Go</button>
<script>
setTimeout(() => {
  const next = document.createElement('button');
  next.id = 'go';
  next.textContent = 'Go';
  const old = document.getElementById('go');
  if (old) old.replaceWith(next);
}, 80);
</script>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(server.Close)

	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	snap, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// Force the original node out of the tree, then wait via the stale ref.
	el, err := WaitForTarget(page, "@1", snap, StateVisible, 2*time.Second)
	if err != nil {
		t.Fatalf("wait ref after rerender: %v", err)
	}
	if el == nil {
		t.Fatal("expected element")
	}
}
