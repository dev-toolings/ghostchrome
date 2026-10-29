package engine

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// Use production launcher flags: testBrowser disables site isolation through
// Rod's defaults and therefore cannot exercise out-of-process frames.
func TestFrameExtractionAndInput(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	for _, mode := range []string{"same-process", "oopif", "nested-oopif"} {
		t.Run(mode, func(t *testing.T) { testFrameExtractionAndInput(t, mode) })
	}
}

func testFrameExtractionAndInput(t *testing.T, mode string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/inner", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body><script>
document.body.attachShadow({mode:'closed'}).innerHTML='<label><input type="checkbox" id="check" style="opacity:0">Frame checkbox</label>';
window.events=[]; for(const k of ['click','keydown']) document.addEventListener(k,e=>events.push({type:k,trusted:e.isTrusted}));</script>`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	otherOrigin := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	if mode == "same-process" {
		otherOrigin = server.URL
	}
	path := "/inner"
	if mode == "nested-oopif" {
		path = "/middle"
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `<!doctype html><iframe src="/inner"></iframe>`)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!doctype html><button>Outside</button><label><input type="checkbox">Frame checkbox</label><section id="widget"></section>
<script>document.querySelector('#widget').attachShadow({mode:'closed'}).innerHTML='<iframe title="Embedded form" src="%s%s"></iframe>';</script>`, otherOrigin, path)
	})
	l := NewLauncher(LauncherOpts{Headless: true, UserDataDir: filepath.Join(t.TempDir(), "profile")}).Leakless(false)
	endpoint, err := l.Launch()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Cleanup()
	defer l.Kill()
	b, err := NewBrowser(endpoint, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	page, err := b.Page()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Navigate(page, server.URL, "load"); err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"", "#widget"} {
		t.Run("scope="+selector, func(t *testing.T) {
			result, err := Extract(page, LevelSkeleton, selector, false)
			if err != nil {
				t.Fatal(err)
			}
			if selector != "" && (hasName(result, "Outside") || len(result.Warnings) != 0) {
				t.Fatalf("scope leaked or fell back: names=%v warnings=%v", extractedNames(result), result.Warnings)
			}
			var ref string
			for key, node := range result.Refs {
				if node.Name == "Frame checkbox" && node.FrameID != "" {
					ref = key
				}
			}
			if ref == "" {
				t.Fatalf("missing frame checkbox: %v", extractedNames(result))
			}
			snapshot := &PageSnapshot{Refs: assignRefSnapshots(result.Refs)}
			el, err := ResolveRef(page, ref, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if (el.Page().SessionID != page.SessionID) != (mode != "same-process") {
				t.Fatal("unexpected frame session routing")
			}
			if err := ClickRef(page, ref, snapshot); err != nil {
				t.Fatal(err)
			}
			checked, err := el.Eval(`() => this.checked`)
			if err != nil || !checked.Value.Bool() {
				t.Fatalf("mouse did not check checkbox: %v %v", checked, err)
			}
			if err := PressKey(page, "Space", "", snapshot); err != nil {
				t.Fatal(err)
			}
			state, err := el.Eval(`() => !this.checked && events.some(e=>e.type==='click'&&e.trusted) && events.some(e=>e.type==='keydown'&&e.trusted)`)
			if err != nil || !state.Value.Bool() {
				t.Fatalf("keyboard/trusted input failed: %v %v", state, err)
			}
			if _, err := el.Eval(`() => { this.outerHTML = this.outerHTML }`); err != nil {
				t.Fatal(err)
			}
			// Force semantic fallback after replacement, still in this frame.
			if err := PressKey(page, "Space", ref, snapshot); err != nil {
				t.Fatal(err)
			}
			fresh, err := ResolveRef(page, ref, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			checked, err = fresh.Eval(`() => { const checked = this.checked; this.checked = false; return checked }`)
			if err != nil || !checked.Value.Bool() {
				t.Fatalf("semantic fallback targeted wrong document: %v %v", checked, err)
			}
			if _, err := fresh.Eval(`() => {
				const cover = document.createElement('div'); cover.id = 'cover';
				cover.style = 'position:fixed;inset:0;z-index:999;background:white';
				this.getRootNode().append(cover);
			}`); err != nil {
				t.Fatal(err)
			}
			if err := checkActionable(fresh); err == nil || !strings.Contains(err.Error(), "covered by") {
				t.Fatalf("covered transparent input accepted: %v", err)
			}
			if _, err := fresh.Eval(`() => this.getRootNode().querySelector('#cover').remove()`); err != nil {
				t.Fatal(err)
			}
			attempts := 0
			err = withSemanticRetry(page, ref, snapshot, func(target *rod.Element) error {
				attempts++
				if target.Page().FrameID != snapshot.Refs[ref].FrameID {
					return errors.New("retry escaped the original frame")
				}
				if attempts == 1 {
					if _, err := target.Eval(`() => { this.outerHTML = this.outerHTML }`); err != nil {
						return err
					}
					return errors.New("element is detached")
				}
				return nil
			})
			if err != nil || attempts != 2 {
				t.Fatalf("frame-local action retry: attempts=%d err=%v", attempts, err)
			}
		})
	}
	// A removed frame must never resolve its ref against a same-named control
	// in the parent document.
	snapshot := &PageSnapshot{Refs: map[string]RefSnapshot{"@1": {FrameID: proto.PageFrameID("detached"), Role: "button", Name: "Outside"}}}
	if _, err := ResolveRef(page.Timeout(time.Second), "@1", snapshot); err == nil {
		t.Fatal("detached frame ref resolved in the parent document")
	}
}
