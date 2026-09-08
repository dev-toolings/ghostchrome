package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// TestClearDataDomeCookies verifies brique #1 (the clear-retry-once trick):
// clearDataDomeCookies removes only the `datadome` cookie (case-insensitive,
// any domain), leaving unrelated cookies — including Cloudflare's
// cf_clearance, which must stay untouched by this DataDome-specific helper —
// alone. No network access: the cookie is set directly via CDP (the
// browser's own cookie jar), never fetched from a live site.
func TestClearDataDomeCookies(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	l := launcher.New().Headless(true).Leakless(false).
		UserDataDir(filepath.Join(t.TempDir(), "chrome-profile"))
	if needsNoSandbox() {
		l = l.NoSandbox(true)
	}
	controlURL, err := l.Launch()
	if err != nil {
		CleanupFailedLauncher(l, true)
		t.Skipf("launch chrome: %v (skipping: chrome not available)", err)
	}
	defer func() {
		l.Kill()
		l.Cleanup()
	}()

	b := rod.New().ControlURL(controlURL)
	if err := b.Connect(); err != nil {
		t.Skipf("connect: %v (skipping)", err)
	}
	defer b.Close()

	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	if err := b.SetCookies([]*proto.NetworkCookieParam{
		{Name: "datadome", Value: "poisoned", URL: "https://example.com/"},
		{Name: "DataDome", Value: "poisoned-mixed-case", URL: "https://other.example.com/"},
		{Name: "cf_clearance", Value: "keep-me", URL: "https://example.com/"},
		{Name: "session_id", Value: "keep-me-too", URL: "https://example.com/"},
	}); err != nil {
		t.Fatalf("set cookies: %v", err)
	}

	clearDataDomeCookies(page)

	cookies, err := page.Browser().GetCookies()
	if err != nil {
		t.Fatalf("get cookies: %v", err)
	}
	remaining := map[string]bool{}
	for _, c := range cookies {
		remaining[c.Name] = true
		if strings.EqualFold(c.Name, "datadome") {
			t.Errorf("datadome cookie %q (domain %q) not removed", c.Name, c.Domain)
		}
	}
	if !remaining["cf_clearance"] || !remaining["session_id"] {
		t.Errorf("clearDataDomeCookies must not remove unrelated cookies, got %v", remaining)
	}
}
