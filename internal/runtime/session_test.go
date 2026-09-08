package runtime

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
)

// localChromeConfig returns a Config that launches a throwaway headless Chrome
// under a temp profile — no named session, no daemon, no --connect.
func localChromeConfig(t *testing.T) Config {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "chrome")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return Config{
		BrowserOpts: func() engine.BrowserOpts {
			return engine.BrowserOpts{Headless: true, TimeoutSec: 15, UserDataDir: dir}
		},
		TimeoutSec: 15,
	}
}

func TestAgentRejectsMalformedArgsBeforeOpeningBrowser(t *testing.T) {
	for op, raw := range map[string]string{"scroll_to": `{"y":"bad"}`, "screenshot": `{"full_page":"bad"}`, "wait": `{"ms":"bad"}`} {
		t.Run(op, func(t *testing.T) {
			s := New(Config{})
			if _, err := s.Dispatch(op, json.RawMessage(raw)); err == nil {
				t.Fatal("expected argument error")
			}
			if s.browser != nil {
				s.Shutdown()
				t.Fatal("invalid arguments opened a browser")
			}
		})
	}
}

// TestDispatchRejectsUnknownOp locks the closed-world behaviour of the dispatch
// table: an op absent from Ops() must fail, never silently no-op.
func TestDispatchRejectsUnknownOp(t *testing.T) {
	if Handles("teleport") {
		t.Fatal("teleport should not be a JSONL op")
	}
	if _, err := New(Config{}).Dispatch("teleport", nil); err == nil {
		t.Fatal("expected an error for an unknown op")
	}
	for _, op := range Ops() {
		if !Handles(op) {
			t.Errorf("Ops() lists %q but Handles reports it unhandled", op)
		}
	}
}

func TestAgentRetainsDialogPolicyAndErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	t.Setenv("HOME", t.TempDir())
	s := New(localChromeConfig(t))
	t.Cleanup(s.Shutdown)
	if _, err := s.Dispatch("dialog", json.RawMessage(`{"action":"dismiss"}`)); err != nil {
		t.Fatal(err)
	}
	_, page, err := s.EnsurePage()
	if err != nil {
		t.Fatal(err)
	}
	if accept, _ := s.dialogPolicy.Snapshot(); accept {
		t.Fatal("initialization replaced dismiss policy")
	}
	if _, err := engine.Navigate(page, "data:text/html,"+url.PathEscape(`<script>document.title=String(confirm('test'))</script>`), "load"); err != nil {
		t.Fatal(err)
	}
	info, err := page.Info()
	if err != nil || info.Title != "false" {
		t.Fatalf("dialog was not dismissed: %+v, %v", info, err)
	}
	if _, err := page.Eval(`() => console.error("retained-error")`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		result, err := s.opErrors(nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range result.([]engine.ErrorEntry) {
			if entry.Message == "retained-error" {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("errors lost previous console event")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// An observation timeout after a completed action must never advise retry.
	ctx, cancel := context.WithCancel(page.GetContext())
	cancel()
	for _, mode := range []engine.SnapshotMode{engine.SnapshotModeFull, engine.SnapshotModeDiff} {
		result, err := s.mutationResult(s.browser, page.Context(ctx), nil, mode)
		if err == nil || result != nil {
			t.Fatalf("observation failure became success: %v, %v", result, err)
		}
		if _, retry := engine.ClassifyError(err); retry {
			t.Fatal("completed action is retryable")
		}
	}
}

// A mutation must advance the ref table to the state it just observed.
// Browser.Snapshot is only populated in connected mode, so with an embedded
// Chrome it returns nil and the table used to stay pinned to whatever the last
// "extract" left behind. Every following mutation then diffed the fresh
// skeleton against that stale content-level table and re-reported the same
// difference forever.
func TestMutationAdvancesRefsWithEmbeddedChrome(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	t.Setenv("HOME", t.TempDir())
	// Not t.TempDir for the profile: Chrome may still be releasing it when the
	// cleanup runs, and a failed RemoveAll would fail an otherwise green test.
	dir, err := os.MkdirTemp("", "gc-runtime-refs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	s := New(Config{
		BrowserOpts: func() engine.BrowserOpts {
			return engine.BrowserOpts{Headless: true, TimeoutSec: 15, UserDataDir: dir}
		},
		TimeoutSec: 15,
	})
	t.Cleanup(s.Shutdown)

	page := "data:text/html," + url.PathEscape(`<input id=i type=text><button id=b>Go</button>`)
	args, err := json.Marshal(map[string]string{"url": page})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch("navigate", args); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch("extract", nil); err != nil {
		t.Fatal(err)
	}
	if s.browser.Snapshot(s.page) != nil {
		t.Skip("browser is in connected mode; this regression only affects embedded Chrome")
	}
	afterExtract := s.Refs()
	if afterExtract == nil {
		t.Fatal("extract left no ref table")
	}
	if _, err := s.Dispatch("click", json.RawMessage(`{"ref":"@2"}`)); err != nil {
		t.Fatal(err)
	}
	if s.Refs() == afterExtract {
		t.Fatal("the ref table still points at the extract; later diffs would keep comparing against it")
	}
}
