package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWaitForPageDOMContentLoadedAlreadyReady(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>ready</title><body>ok</body>"))
	}))
	t.Cleanup(server.Close)
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- WaitForPage(page, "domcontentloaded") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForPage: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForPage(domcontentloaded) hung on an already-loaded page")
	}
}

func TestClickSameTabLinkWaitsForLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><a href="/next">Go</a>`))
	})
	mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>next</title><h1>Arrived</h1>`))
	})
	server := httptest.NewServer(mux)
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
	if err := ClickRef(page, "@1", snap); err != nil {
		t.Fatalf("click: %v", err)
	}
	if err := WaitForText(page, "Arrived", time.Second); err != nil {
		t.Fatalf("after click: %v", err)
	}
}

func TestClickButtonSkipsNavWait(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><button>Go</button>`))
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
	start := time.Now()
	if err := ClickRef(page, "@1", snap); err != nil {
		t.Fatalf("click: %v", err)
	}
	if ClickNavHint(page) {
		t.Fatal("plain button must not set nav hint")
	}
	if time.Since(start) > 400*time.Millisecond {
		t.Fatalf("button click waited too long: %s", time.Since(start))
	}
}

func TestClickSPAPushStateWaitsForURL(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	html := `<!doctype html><a id=go href="/next">Go</a><script>
	document.getElementById('go').addEventListener('click', (e) => {
	  e.preventDefault();
	  history.pushState({}, '', '/next');
	  document.body.innerHTML = '<h1>Arrived</h1>';
	});
	</script>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(server.Close)
	_, page := newIsolatedPage(t)
	if _, err := StartEventHub(page); err != nil {
		t.Fatalf("hub: %v", err)
	}
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
	if err := ClickRef(page, "@1", snap); err != nil {
		t.Fatalf("click: %v", err)
	}
	if err := WaitForURL(page, "/next", time.Second); err != nil {
		t.Fatalf("spa url: %v", err)
	}
	if err := WaitForText(page, "Arrived", time.Second); err != nil {
		t.Fatalf("spa text: %v", err)
	}
}

func TestReloadPageDoesNotHang(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<h1>Reloaded</h1>"))
	}))
	t.Cleanup(server.Close)
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- ReloadPage(page, "load") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReloadPage hung")
	}
}

func TestSubmitOnElementWaitsForNavigation(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><form action="/next" method="get"><input name="q" value="hi"></form>`))
	})
	mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><h1>Arrived</h1>`))
	})
	server := httptest.NewServer(mux)
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
	el, err := ResolveRef(page, "@1", snap)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := SubmitOnElement(page, el); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := WaitForText(page, "Arrived", time.Second); err != nil {
		t.Fatalf("after submit: %v", err)
	}
}

func TestPressSpaceDoesNotWaitForNav(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><button>Go</button>`))
	}))
	t.Cleanup(server.Close)
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	start := time.Now()
	if err := PressKey(page, "Space", "", nil); err != nil {
		t.Fatalf("press: %v", err)
	}
	if ClickNavHint(page) {
		t.Fatal("Space must not set nav hint")
	}
	if time.Since(start) > 400*time.Millisecond {
		t.Fatalf("Space waited too long: %s", time.Since(start))
	}
}

func TestClickIframeLinkWaitsForFrameNav(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><iframe src="/inner"></iframe>`))
	})
	mux.HandleFunc("/inner", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><a href="/next">Go</a>`))
	})
	mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><h1>InnerArrived</h1>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	_, page := newIsolatedPage(t)
	if _, err := StartEventHub(page); err != nil {
		t.Fatalf("hub: %v", err)
	}
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	iframe, err := page.Timeout(3 * time.Second).Element("iframe")
	if err != nil {
		t.Fatalf("iframe: %v", err)
	}
	frame, err := iframe.Frame()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	el, err := frame.Timeout(3 * time.Second).Element("a")
	if err != nil {
		t.Fatalf("iframe link: %v", err)
	}
	// Wait on the main page hub; iframe pages do not have their own EventHub.
	if err := ClickElement(page, el); err != nil {
		t.Fatalf("click: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		res, err := page.Eval(`() => {
			const f = document.querySelector('iframe');
			try { return !!(f && f.contentDocument && f.contentDocument.body && f.contentDocument.body.innerText.includes('InnerArrived')); } catch (e) { return false; }
		}`)
		if err == nil && res != nil && fmt.Sprint(res.Value.Val()) == "true" {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("iframe after click: InnerArrived not found")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestClickDownloadAttributeDoesNotHang(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	html := `<!doctype html><a href="/file.csv" download>GetCSV</a>`
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html))
	})
	mux.HandleFunc("/file.csv", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=file.csv")
		_, _ = w.Write([]byte("a,b\n1,2\n"))
	})
	server := httptest.NewServer(mux)
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
	done := make(chan error, 1)
	go func() { done <- ClickRef(page, "@1", snap) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("click download: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("download click hung")
	}
	if !ClickDownloadHint(page) {
		t.Fatal("expected download hint on download= link")
	}
}

func TestClickFileInputDoesNotHang(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	html := `<!doctype html><form><input type="file" aria-label="Resume"></form>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(server.Close)
	_, page := newIsolatedPage(t)
	StartFileChooserIntercept(page)
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
			t.Fatalf("click file input: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("file input click hung on native chooser")
	}
	if ClickNavHint(page) {
		t.Fatal("file input must not be treated as navigation")
	}
}

func TestFileChooserSurvivesNavigation(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><a href="/upload">Go</a>`))
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><form><input type="file" aria-label="Resume"></form>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	_, page := newIsolatedPage(t)
	StartFileChooserIntercept(page)
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
	if err := ClickRef(page, "@1", snap); err != nil {
		t.Fatalf("click link: %v", err)
	}
	result, err = Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract 2: %v", err)
	}
	snap, err = BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("snapshot 2: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- ClickRef(page, "@1", snap) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("click file after nav: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("file input click after navigation hung")
	}
}

func TestExtractClickNestedIframeRef(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><iframe src="/mid"></iframe>`))
	})
	mux.HandleFunc("/mid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><iframe src="/inner"></iframe>`))
	})
	mux.HandleFunc("/inner", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><a href="/next">DeepGo</a>`))
	})
	mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><h1>DeepArrived</h1>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	_ = page.Timeout(2 * time.Second).WaitStable(300 * time.Millisecond)
	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	var innerRef string
	for ref, node := range result.Refs {
		if node.Name == "DeepGo" {
			innerRef = ref
			break
		}
	}
	if innerRef == "" {
		t.Fatalf("expected nested iframe link ref, refs=%v", result.Refs)
	}
	snap, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := ClickRef(page, innerRef, snap); err != nil {
		t.Fatalf("click nested iframe ref: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		res, err := page.Eval(`() => {
			try {
				const a = document.querySelector('iframe');
				const b = a && a.contentDocument && a.contentDocument.querySelector('iframe');
				const doc = b && b.contentDocument;
				return !!(doc && doc.body && doc.body.innerText.includes('DeepArrived'));
			} catch (e) { return false; }
		}`)
		if err == nil && res != nil && fmt.Sprint(res.Value.Val()) == "true" {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatal("nested iframe @ref click did not navigate")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestExtractOpenShadowButton(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	html := `<!doctype html><div id=h></div><script>
	const r = document.getElementById('h').attachShadow({mode:'open'});
	r.innerHTML = '<button>ShadowGo</button>';
	</script>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
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
	var ref string
	for k, n := range result.Refs {
		if n.Name == "ShadowGo" {
			ref = k
			break
		}
	}
	if ref == "" {
		t.Fatalf("expected shadow button ref, refs=%v", result.Refs)
	}
	snap, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := ClickRef(page, ref, snap); err != nil {
		t.Fatalf("click shadow ref: %v", err)
	}
}

func TestClickBelowFoldScrollsIntoView(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	html := `<!doctype html><body style="margin:0"><div style="height:3000px"></div><button id=go>Below</button></body>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
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
	var ref string
	for k, n := range result.Refs {
		if n.Name == "Below" {
			ref = k
			break
		}
	}
	if ref == "" {
		t.Fatalf("expected Below ref, refs=%v", result.Refs)
	}
	if err := ClickRef(page, ref, snap); err != nil {
		t.Fatalf("click below fold: %v", err)
	}
	y, err := page.Eval(`() => window.scrollY`)
	if err != nil {
		t.Fatalf("scrollY: %v", err)
	}
	if y == nil {
		t.Fatal("empty scrollY")
	}
	got := 0.0
	switch v := y.Value.Val().(type) {
	case float64:
		got = v
	case int:
		got = float64(v)
	}
	if got < 100 {
		t.Fatalf("expected page to scroll, scrollY=%v", y.Value.Val())
	}
}

func TestExtractClickIframeRef(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><button>Outer</button><iframe src="/inner"></iframe>`))
	})
	mux.HandleFunc("/inner", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><a href="/next">InnerGo</a>`))
	})
	mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><h1>InnerArrived</h1>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	_ = page.Timeout(2 * time.Second).WaitStable(200 * time.Millisecond)
	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	var innerRef string
	for ref, node := range result.Refs {
		if node.Name == "InnerGo" {
			innerRef = ref
			break
		}
	}
	if innerRef == "" {
		t.Fatalf("expected iframe link ref, refs=%v", result.Refs)
	}
	snap, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.Refs[innerRef].FrameID == "" {
		t.Fatal("iframe ref missing FrameID")
	}
	if err := ClickRef(page, innerRef, snap); err != nil {
		t.Fatalf("click iframe ref: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		res, err := page.Eval(`() => {
			const f = document.querySelector('iframe');
			try { return !!(f && f.contentDocument && f.contentDocument.body && f.contentDocument.body.innerText.includes('InnerArrived')); } catch (e) { return false; }
		}`)
		if err == nil && res != nil && fmt.Sprint(res.Value.Val()) == "true" {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatal("iframe @ref click did not navigate")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestWaitForTextAndURL(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>dash</title><body>Ready now</body>"))
	}))
	t.Cleanup(server.Close)
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL+"/dash", "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if err := WaitForText(page, "Ready", time.Second); err != nil {
		t.Fatalf("text: %v", err)
	}
	if err := WaitForURL(page, "/dash", time.Second); err != nil {
		t.Fatalf("url: %v", err)
	}
}

func TestWaitForPageIdleDoesNotHangOnOpenRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	hung := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hang" {
			<-hung
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><body><script>fetch('/hang')</script>ok</body>`))
	}))
	t.Cleanup(func() { close(hung); server.Close() })
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	timed := page.Timeout(800 * time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- WaitForPage(timed, "idle") }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("WaitForPage(idle) hung on an open request")
	}
}

func TestClickFileInputInIframeDoesNotHang(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><iframe src="/upload"></iframe>`))
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><form><input type="file" aria-label="Resume"></form>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	_ = page.Timeout(2 * time.Second).WaitStable(200 * time.Millisecond)
	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	var fileRef string
	for ref, node := range result.Refs {
		if node.Name == "Resume" {
			fileRef = ref
			break
		}
	}
	if fileRef == "" {
		t.Fatalf("expected iframe file input ref, refs=%v", result.Refs)
	}
	snapshot, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Refs[fileRef].FrameID == "" {
		t.Fatal("iframe file input ref missing FrameID")
	}

	done := make(chan error, 1)
	go func() { done <- ClickRef(page, fileRef, snapshot) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("click iframe file input: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("iframe file input click hung on native chooser")
	}
	if ClickNavHint(pageForFrame(page, snapshot.Refs[fileRef].FrameID)) {
		t.Fatal("iframe file input must not be treated as navigation")
	}
}

func TestClickFileInputInIframeAfterNavigationDoesNotHang(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><iframe src="/start"></iframe>`))
	})
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><a href="/upload">Continue</a>`))
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><form><input type="file" aria-label="Resume"></form>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	_ = page.Timeout(2 * time.Second).WaitStable(200 * time.Millisecond)
	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract start frame: %v", err)
	}
	var continueRef string
	for ref, node := range result.Refs {
		if node.Name == "Continue" {
			continueRef = ref
			break
		}
	}
	if continueRef == "" {
		t.Fatalf("expected iframe navigation ref, refs=%v", result.Refs)
	}
	snapshot, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("snapshot start frame: %v", err)
	}
	if err := ClickRef(page, continueRef, snapshot); err != nil {
		t.Fatalf("navigate iframe: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		ready, err := page.Eval(`() => !!document.querySelector('iframe')?.contentDocument?.querySelector('input[type=file]')`)
		if err == nil && ready != nil && fmt.Sprint(ready.Value.Val()) == "true" {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("iframe did not navigate to the upload form")
		}
		time.Sleep(25 * time.Millisecond)
	}

	result, err = Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract upload frame: %v", err)
	}
	var fileRef string
	for ref, node := range result.Refs {
		if node.Name == "Resume" {
			fileRef = ref
			break
		}
	}
	if fileRef == "" {
		t.Fatalf("expected iframe file input ref after navigation, refs=%v", result.Refs)
	}
	snapshot, err = BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("snapshot upload frame: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- ClickRef(page, fileRef, snapshot) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("click iframe file input after navigation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("iframe file input click after navigation hung on native chooser")
	}
	if ClickNavHint(pageForFrame(page, snapshot.Refs[fileRef].FrameID)) {
		t.Fatal("iframe file input after navigation must not be treated as navigation")
	}
}
