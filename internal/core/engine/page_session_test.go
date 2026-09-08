package engine

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolveRefSemanticFallsBackAfterRerender(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	html := `<!doctype html><html><body>
<button id="go">Go</button>
<script>
document.getElementById('go').addEventListener('click', () => {
  const next = document.createElement('button');
  next.id = 'go';
  next.textContent = 'Go';
  document.getElementById('go').replaceWith(next);
});
</script>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(server.Close)

	b, page := newIsolatedPage(t)
	rt := NewRuntime(b)
	ps := rt.PageSession(page)
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
	if err := ps.ClickRef("@1", snap); err != nil {
		t.Fatalf("first click: %v", err)
	}
	start := time.Now()
	if err := ps.ClickRef("@1", snap); err != nil {
		t.Fatalf("semantic fallback click: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("semantic fallback took %s, expected a bounded probe not a full action timeout", elapsed)
	}
}

func TestResolveRefSemanticAmbiguous(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	// Three identical buttons: @3 is nth=2. After click the page keeps only two,
	// so role+name still matches but nth is out of range → ambiguous, not a guess.
	html := `<!doctype html><html><body>
<button>Go</button><button>Go</button><button id="last">Go</button>
<script>
document.getElementById('last').addEventListener('click', () => {
  document.body.innerHTML = '<button>Go</button><button>Go</button>';
});
</script>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(server.Close)

	b, page := newIsolatedPage(t)
	rt := NewRuntime(b)
	ps := rt.PageSession(page)
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
	if err := ps.ClickRef("@3", snap); err != nil {
		t.Fatalf("first click: %v", err)
	}
	err = ps.ClickRef("@3", snap)
	if !errors.Is(err, ErrAmbiguousRef) {
		t.Fatalf("want ambiguous ref, got %v", err)
	}
}
func TestSetCheckedRefFallsBackAfterRerender(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	html := `<!doctype html><html><body>
<input type=checkbox id=c aria-label=Agree>
<script>
document.getElementById('c').addEventListener('click', () => {
  const next = document.createElement('input');
  next.type = 'checkbox';
  next.id = 'c';
  next.setAttribute('aria-label', 'Agree');
  document.getElementById('c').replaceWith(next);
}, {once: true});
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
	if err := SetCheckedRef(page, "@1", true, snap); err != nil {
		t.Fatalf("first check: %v", err)
	}
	start := time.Now()
	if err := SetCheckedRef(page, "@1", true, snap); err != nil {
		t.Fatalf("semantic fallback check: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("semantic fallback took %s", elapsed)
	}
}

func TestListAndSwitchTabs(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	b, page := newIsolatedPage(t)
	if _, err := Navigate(page, "about:blank", "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	tabs, err := ListTabs(b.RodBrowser(), string(page.TargetID))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tabs) == 0 {
		t.Fatal("expected at least one tab")
	}
	active := 0
	for _, tab := range tabs {
		if tab.Active {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("active tabs=%d want 1", active)
	}
	second, err := NewTab(b.RodBrowser(), "about:blank")
	if err != nil {
		t.Fatalf("new tab: %v", err)
	}
	if err := b.SetCurrentPage(second); err != nil {
		t.Fatalf("set current: %v", err)
	}
	tabs, err = ListTabs(b.RodBrowser(), string(second.TargetID))
	if err != nil {
		t.Fatalf("list after new: %v", err)
	}
	if len(tabs) < 2 {
		t.Fatalf("tabs=%d want >=2", len(tabs))
	}
}

func TestFillFieldsRefreshesSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	html := `<!doctype html><html><body>
<input aria-label=Country>
<input aria-label=City disabled>
<script>
document.querySelector('input[aria-label=Country]').addEventListener('input', (e) => {
  const city = document.querySelector('input[aria-label=City]');
  city.disabled = false;
  city.replaceWith(city.cloneNode(true));
});
</script>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(server.Close)

	b, page := newIsolatedPage(t)
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
	filled, snap, err := FillFields(b, page, map[string]string{"@1": "FR", "@2": "Paris"}, snap)
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if filled != 2 {
		t.Fatalf("filled=%d", filled)
	}
	if snap == nil {
		t.Fatal("expected refreshed snapshot")
	}
}

func TestDialogAutoHandlerAcceptsAlert(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	html := `<!doctype html><html><body>
<button id=go onclick="alert('hi')">Go</button>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(server.Close)

	_, page := newIsolatedPage(t)
	policy := &DialogAutoPolicy{Accept: true}
	StartDialogAutoHandler(page, policy)
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
	done := make(chan error, 1)
	go func() { done <- ClickRef(page, "@1", snap) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("click with alert: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("click blocked on alert")
	}
}

func TestDialogAutoHandlerAcceptsTwoAlerts(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	html := `<!doctype html><html><body>
<button id=go onclick="alert('one'); alert('two')">Go</button>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(server.Close)

	_, page := newIsolatedPage(t)
	StartDialogAutoHandler(page, &DialogAutoPolicy{Accept: true})
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
	done := make(chan error, 1)
	go func() { done <- ClickRef(page, "@1", snap) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("click with two alerts: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("click blocked on second alert")
	}
}

func TestArmPopupWaitAdoptsBlankTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	html := `<!doctype html><html><body>
<a id=go href="about:blank" target="_blank">Go</a>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(server.Close)

	b, page := newIsolatedPage(t)
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
	mark := PopupMark(page)
	if err := ClickRef(page, "@1", snap); err != nil {
		t.Fatalf("click: %v", err)
	}
	popup := AdoptClickPopup(page, mark, snap, "@1")
	if popup == nil {
		t.Fatal("expected popup page")
	}
	if popup.TargetID == page.TargetID {
		t.Fatal("popup should be a new target")
	}
	_ = b
}
