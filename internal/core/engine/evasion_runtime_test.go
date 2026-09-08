package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// detectorHTML runs the classic "Runtime.enable" leak probe on load: it puts a
// getter on an Error's `stack`, then console.debug()s that Error. Chrome only
// serializes console arguments into RemoteObject previews (which reads the
// getter) when a CDP client has issued `Runtime.enable`. So window.__rt ends up
// true iff Runtime.enable is active — exactly what DataDome & co. probe for.
const detectorHTML = `<!doctype html><html><body><script>
window.__rt = false;
const e = new Error();
Object.defineProperty(e, 'stack', { configurable: true, get() { window.__rt = true; return 'x'; } });
console.debug(e);
</script></body></html>`

// runtimeEnableLeaks launches a fresh browser (Runtime.enable is per-page and
// sticky once sent, so cases must not share a page), attaches the error
// collector the way `snapshot` does, loads the probe, and reports window.__rt.
func runtimeEnableLeaks(t *testing.T, evade bool) bool {
	t.Helper()
	SetEvadeRuntimeEnable(evade)
	defer SetEvadeRuntimeEnable(false)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(detectorHTML))
	}))
	defer server.Close()

	b, cleanup := testBrowser(t)
	defer cleanup()

	page, err := b.Page()
	if err != nil {
		t.Fatalf("page: %v", err)
	}

	// Attach the collector BEFORE navigating (as CollectErrors/snapshot do), so
	// Runtime.enable — if not evaded — is in force when the probe script runs.
	c := NewErrorCollector(page)
	defer c.Close()
	time.Sleep(200 * time.Millisecond) // let the (possible) Runtime.enable land

	if _, err := Navigate(page, server.URL, "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	time.Sleep(300 * time.Millisecond) // let console.debug + CDP roundtrip settle

	res, err := page.Eval(`() => window.__rt === true`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	return res.Value.Bool()
}

func TestEvadeRuntimeSuppressesRuntimeEnable(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	// Hard guarantee: with evasion ON, Runtime.enable is never sent, so the
	// probe can never fire — deterministic regardless of Chrome version.
	if runtimeEnableLeaks(t, true) {
		t.Error("evasion ON: Runtime.enable leak detected (want suppressed)")
	}

	// Differential: with evasion OFF the leak should be observable. If this
	// Chrome build doesn't exhibit it, the probe is N/A here — skip rather than
	// fail, but the safety property above still stands.
	if !runtimeEnableLeaks(t, false) {
		t.Skip("Chrome did not exhibit the Runtime.enable leak with evasion OFF; differential unverifiable in this env")
	}
}
