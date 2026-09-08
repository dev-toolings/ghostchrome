package engine

import (
	"os"
	"testing"
)

// lacentraleFixtureHTML loads the real DataDome interstitial captured from
// https://www.lacentrale.fr/listing (headful, stealth, --user-profile
// ddspike). The IP had already accumulated several hits that day, so
// DataDome escalated straight to its visual/interactive CAPTCHA ('t':'bv')
// rather than the silent JS-only check — this is the actual signature
// ghostchrome must recognize, not a "Just a moment"-style text interstitial.
func lacentraleFixtureHTML(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/lacentrale_listing.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(data)
}

func TestClassifyBotChallengeHTML_DataDomeCaptcha(t *testing.T) {
	html := lacentraleFixtureHTML(t)
	matched, ambient := classifyBotChallengeHTML("https://www.lacentrale.fr/listing", "lacentrale.fr", html)
	if !matched {
		t.Fatalf("expected the lacentrale DataDome CAPTCHA fixture to match as a bot challenge (ambient=%v)", ambient)
	}
}

func TestIsInteractiveDataDomeCaptcha_LaCentraleFixture(t *testing.T) {
	html := lacentraleFixtureHTML(t)
	if !isInteractiveDataDomeCaptcha(html) {
		t.Fatal("expected the lacentrale fixture (geo.captcha-delivery.com/captcha iframe) to be classified as an interactive CAPTCHA")
	}
}

// TestClassifyBotChallengeHTML_NoRegressionOnCloudflare guards the existing
// Cloudflare detection paths against regressions introduced by the
// classifyBotChallengeHTML refactor (isBotChallenge previously inlined this
// logic directly).
func TestClassifyBotChallengeHTML_NoRegressionOnCloudflare(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		title   string
		html    string
		matched bool
		ambient bool
	}{
		{
			name:    "just a moment title",
			url:     "https://example.com/",
			title:   "Just a moment...",
			html:    "<html></html>",
			matched: true,
		},
		{
			name:    "attention required title",
			url:     "https://example.com/",
			title:   "Attention Required! | Cloudflare",
			html:    "<html></html>",
			matched: true,
		},
		{
			name:    "cf challenge running marker",
			url:     "https://example.com/",
			title:   "example",
			html:    "<html><script>cf-challenge-running</script></html>",
			matched: true,
		},
		{
			name:    "ambient turnstile marker alone is not decisive",
			url:     "https://example.com/",
			title:   "example",
			html:    "<html><script src=\"https://challenges.cloudflare.com/turnstile.js\"></script></html>",
			matched: false,
			ambient: true,
		},
		{
			name:    "ordinary page",
			url:     "https://example.com/",
			title:   "example",
			html:    "<html><body><h1>hello</h1></body></html>",
			matched: false,
			ambient: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, ambient := classifyBotChallengeHTML(tc.url, tc.title, tc.html)
			if matched != tc.matched || ambient != tc.ambient {
				t.Fatalf("classifyBotChallengeHTML(%q, %q) = (%v, %v), want (%v, %v)",
					tc.url, tc.title, matched, ambient, tc.matched, tc.ambient)
			}
		})
	}
}

// TestIsInteractiveDataDomeCaptcha_NotAmbientTag guards against a false
// positive on the plain DataDome SDK tag (present on virtually every
// DataDome-protected page, including ones that pass through silently) — only
// the actual captcha iframe must be classified as unsolvable-by-waiting.
func TestIsInteractiveDataDomeCaptcha_NotAmbientTag(t *testing.T) {
	html := `<html><head><script src="https://ct.captcha-delivery.com/c.js"></script></head><body>real content</body></html>`
	if isInteractiveDataDomeCaptcha(html) {
		t.Fatal("plain DataDome SDK tag must not be classified as an interactive captcha")
	}
}

// TestDecideReloadStrategy_DataDomeClearRetryOnce is the pure-logic guard for
// the "clear the datadome cookie and retry once" trick: on a wedged, non-
// interactive DataDome challenge it must select reloadStrategyDataDome
// exactly once (datadomeCleared=false), then fall back to the Cloudflare
// branch on every subsequent tick (datadomeCleared=true) — it must never
// select reloadStrategyDataDome twice in a row.
func TestDecideReloadStrategy_DataDomeClearRetryOnce(t *testing.T) {
	const url = "https://www.lacentrale.fr/listing"
	const html = `<html><head><script src="https://ct.captcha-delivery.com/c.js"></script></head><body></body></html>`

	first := decideReloadStrategy(url, html, false)
	if first != reloadStrategyDataDome {
		t.Fatalf("first wedged tick: got strategy %v, want reloadStrategyDataDome", first)
	}

	second := decideReloadStrategy(url, html, true)
	if second == reloadStrategyDataDome {
		t.Fatal("clear-retry must not fire twice — expected fallback to reloadStrategyCloudflare once datadomeCleared=true")
	}
	if second != reloadStrategyCloudflare {
		t.Fatalf("second wedged tick: got strategy %v, want reloadStrategyCloudflare", second)
	}
}

// TestDecideReloadStrategy_InteractiveCaptchaSkipsReload verifies the
// existing behaviour is preserved: the interactive visual CAPTCHA always
// skips reload, regardless of the DataDome clear-retry state.
func TestDecideReloadStrategy_InteractiveCaptchaSkipsReload(t *testing.T) {
	html := `<html><body><iframe src="https://geo.captcha-delivery.com/captcha/?cid=x"></iframe></body></html>`
	for _, cleared := range []bool{false, true} {
		if got := decideReloadStrategy("https://example.com/", html, cleared); got != reloadStrategySkip {
			t.Fatalf("datadomeCleared=%v: got strategy %v, want reloadStrategySkip", cleared, got)
		}
	}
}

// TestDecideReloadStrategy_CloudflareFallback ensures a wedged challenge with
// no DataDome marker at all keeps using the pre-existing Cloudflare branch.
func TestDecideReloadStrategy_CloudflareFallback(t *testing.T) {
	html := `<html><script>cf-challenge-running</script></html>`
	if got := decideReloadStrategy("https://example.com/", html, false); got != reloadStrategyCloudflare {
		t.Fatalf("got strategy %v, want reloadStrategyCloudflare", got)
	}
}

// TestIsDataDomeMarker covers the URL-only and HTML-only detection paths
// (both must work — e.g. WaitForBotChallenge falls back to url-only when
// page.HTML() itself fails mid-challenge).
func TestIsDataDomeMarker(t *testing.T) {
	cases := []struct {
		name string
		url  string
		html string
		want bool
	}{
		{"url marker", "https://geo.captcha-delivery.com/captcha/", "", true},
		{"html marker", "https://example.com/", `<script src="https://ct.captcha-delivery.com/c.js"></script>`, true},
		{"neither", "https://example.com/", "<html></html>", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDataDomeMarker(tc.url, tc.html); got != tc.want {
				t.Errorf("isDataDomeMarker(%q, %q) = %v, want %v", tc.url, tc.html, got, tc.want)
			}
		})
	}
}

// TestResolveStealthTimezone covers brique #2's locale->timezone default:
// an explicit override always wins; a French locale defaults to
// Europe/Paris; anything else (including the en-US fallback) leaves the
// timezone unset so the host timezone is kept.
func TestResolveStealthTimezone(t *testing.T) {
	cases := []struct {
		name     string
		explicit string
		locale   string
		want     string
	}{
		{"explicit override wins", "America/New_York", "fr-FR", "America/New_York"},
		{"fr-FR defaults to Europe/Paris", "", "fr-FR", "Europe/Paris"},
		{"bare fr defaults to Europe/Paris", "", "fr", "Europe/Paris"},
		{"en-US has no default", "", "en-US", ""},
		{"unknown locale has no default", "", "de-DE", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveStealthTimezone(tc.explicit, tc.locale); got != tc.want {
				t.Errorf("resolveStealthTimezone(%q, %q) = %q, want %q", tc.explicit, tc.locale, got, tc.want)
			}
		})
	}
}
