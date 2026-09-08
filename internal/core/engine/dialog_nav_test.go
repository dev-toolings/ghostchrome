package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDialogAfterNavigationDoesNotHang(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><button onclick="location.href='/next'">Next</button>`))
	})
	mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><button onclick="alert('handled')">Alert</button>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, server.URL, "domcontentloaded"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract first page: %v", err)
	}
	snapshot, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("snapshot first page: %v", err)
	}
	if err := ClickRef(page, "@1", snapshot); err != nil {
		t.Fatalf("click navigation button: %v", err)
	}
	if err := WaitForText(page, "Alert", time.Second); err != nil {
		t.Fatalf("wait for next page: %v", err)
	}

	result, err = Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract next page: %v", err)
	}
	snapshot, err = BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("snapshot next page: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- ClickRef(page, "@1", snapshot) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("click alert button: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("click blocked on dialog after navigation")
	}
}
