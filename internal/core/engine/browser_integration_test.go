package engine

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

func TestBrowserPersistsActiveTabAndSnapshots(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head><title>start</title></head>
  <body>
    <button onclick="document.title='clicked'">Primary action</button>
  </body>
</html>`))
	}))
	defer server.Close()

	l := launcher.New().Headless(true).Leakless(false).
		UserDataDir(filepath.Join(t.TempDir(), "chrome-profile"))
	if needsNoSandbox() {
		l = l.NoSandbox(true)
	}
	controlURL, err := l.Launch()
	if err != nil {
		CleanupFailedLauncher(l, true)
		t.Fatalf("launch browser: %v", err)
	}
	defer func() {
		l.Kill()
		l.Cleanup()
	}()

	statePath, err := sessionStatePath(controlURL)
	if err != nil {
		t.Fatalf("session state path: %v", err)
	}
	_ = os.Remove(statePath)
	defer os.Remove(statePath)

	b, err := NewBrowser(controlURL, true, 10)
	if err != nil {
		t.Fatalf("new browser: %v", err)
	}

	page, err := b.Page()
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if _, err := Navigate(page, server.URL, "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if err := b.SaveSnapshot(page, result); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	firstTargetID := page.TargetID

	secondPage, err := b.browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if _, err := secondPage.Activate(); err != nil {
		t.Fatalf("activate second page: %v", err)
	}
	if err := b.SetCurrentPage(secondPage); err != nil {
		t.Fatalf("persist second page: %v", err)
	}
	secondTargetID := secondPage.TargetID
	b.Close()

	b2, err := NewBrowser(controlURL, true, 10)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer b2.Close()

	currentPage, err := b2.Page()
	if err != nil {
		t.Fatalf("load current page: %v", err)
	}
	if currentPage.TargetID != secondTargetID {
		t.Fatalf("expected current tab %s, got %s", secondTargetID, currentPage.TargetID)
	}

	firstPage, err := b2.browser.PageFromTarget(firstTargetID)
	if err != nil {
		t.Fatalf("reopen first page: %v", err)
	}
	if _, err := firstPage.Activate(); err != nil {
		t.Fatalf("activate first page: %v", err)
	}
	if err := b2.SetCurrentPage(firstPage); err != nil {
		t.Fatalf("set first page current: %v", err)
	}

	snapshot := b2.Snapshot(firstPage)
	if snapshot == nil {
		t.Fatal("expected persisted snapshot for first page")
	}

	if err := ClickRef(firstPage, "@1", snapshot); err != nil {
		t.Fatalf("click ref: %v", err)
	}
	info, err := firstPage.Info()
	if err != nil {
		t.Fatalf("page info: %v", err)
	}
	if info.Title != "clicked" {
		t.Fatalf("expected title to be updated, got %q", info.Title)
	}
}

func TestResolveRefReportsStaleSnapshots(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <body>
    <button id="remove-me">Remove me</button>
  </body>
</html>`))
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

	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	snapshot, err := snapshotFromResult(page, result)
	if err != nil {
		t.Fatalf("snapshot from result: %v", err)
	}

	if _, err := page.Eval(`() => document.getElementById("remove-me").remove()`); err != nil {
		t.Fatalf("remove element: %v", err)
	}

	_, err = ResolveRef(page, "@1", snapshot)
	if !errors.Is(err, ErrStaleRef) {
		t.Fatalf("expected stale ref error, got %v", err)
	}
}

func TestHandleNextDialogWaitsForAndHandlesDialogs(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	b, cleanup := testBrowser(t)
	defer cleanup()

	page, err := b.Page()
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if _, err := Navigate(page, "about:blank", "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = page.Eval(`() => alert("bonjour")`)
	}()

	result, err := HandleNextDialog(page, true, "", 2*time.Second)
	if err != nil {
		t.Fatalf("handle dialog: %v", err)
	}
	if !result.Handled {
		t.Fatalf("expected dialog to be handled, got %+v", result)
	}
	if result.Type != string(proto.PageDialogTypeAlert) {
		t.Fatalf("expected alert dialog, got %+v", result)
	}
	if result.Message != "bonjour" {
		t.Fatalf("expected dialog message bonjour, got %+v", result)
	}
}

func testBrowser(t *testing.T) (*Browser, func()) {
	t.Helper()

	l := launcher.New().Headless(true).Leakless(false).
		UserDataDir(filepath.Join(t.TempDir(), "chrome-profile"))
	if needsNoSandbox() {
		l = l.NoSandbox(true)
	}
	controlURL, err := l.Launch()
	if err != nil {
		CleanupFailedLauncher(l, true)
		t.Fatalf("launch browser: %v", err)
	}

	b, err := NewBrowser(controlURL, true, 10)
	if err != nil {
		l.Kill()
		l.Cleanup()
		t.Fatalf("new browser: %v", err)
	}

	return b, func() {
		b.Close()
		l.Kill()
		l.Cleanup()
	}
}
