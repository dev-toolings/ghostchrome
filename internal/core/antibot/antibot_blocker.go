package antibot

import (
	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/go-rod/rod"
)

// AntiBotPatterns is the curated list of glob URL patterns for scripts whose
// only purpose is detection/fingerprinting/heavy tracking. Blocking them at
// the network layer prevents the main thread from being monopolised by
// fingerprinting bursts that hang Runtime.evaluate calls.
//
// Conservative list — only includes well-known anti-bot vendors and the
// heaviest analytics. Does NOT block CMP frameworks (OneTrust, Cookiebot,
// ...) because some sites' page rendering is gated on consent state.
var AntiBotPatterns = []string{
	// DataDome — fingerprinting + challenge JS.
	"*://js.datadome.co/*",
	"*://*.datado.me/*",
	"*://api-js.datadome.co/*",
	"*://geo.captcha-delivery.com/*",

	// PerimeterX / HUMAN — the same category of fingerprinting JS.
	"*://*.perimeterx.net/*",
	"*://client.perimeterx.net/*",
	"*://collector-pxk*.px-cloud.net/*",

	// Imperva (Incapsula) anti-bot.
	"*://*.imperva.com/v2/*",
	"*://*.incapsula.com/_Incapsula_Resource*",

	// Akamai Bot Manager.
	"*://*.akamaihd.net/akam/*",

	// Heavy ad/tracking SDKs that commonly cause main-thread bursts. These
	// are NOT anti-bot per se but their fingerprinting/auction code competes
	// for CPU and starves Runtime.evaluate the same way.
	"*://googletagservices.com/tag/js/gpt.js*",
	"*://*.googletagservices.com/*",
	"*://securepubads.g.doubleclick.net/*",
	"*://*.googlesyndication.com/*",
	"*://cdn.taboola.com/libtrc/*",
	"*://cdn.outbrain.com/outbrain-loader.js*",
	"*://static.criteo.net/js/*",

	// NOTE — CMP scripts (consentmanager, OneTrust, Cookiebot, Quantcast,
	// Didomi, ...) are intentionally NOT blocked here. They are needed to
	// render AND dismiss the consent modal. Blocking them leaves the
	// server-rendered modal HTML on screen with no JS to handle the click,
	// which is worse than the heavy boot they cause. Use --user-profile to
	// persist consent cookies across sessions instead.
}

// StartAntiBotBlocker installs a request blocker for AntiBotPatterns on the
// given browser. Returns the InterceptSession so the caller can Stop() it
// and read the Stats() to know how many requests were blocked.
//
// extraPatterns lets callers extend the curated list (e.g. site-specific
// trackers known to cause issues).
func StartAntiBotBlocker(browser *rod.Browser, extraPatterns ...string) (*engine.InterceptSession, error) {
	patterns := make([]string, 0, len(AntiBotPatterns)+len(extraPatterns))
	patterns = append(patterns, AntiBotPatterns...)
	patterns = append(patterns, extraPatterns...)
	return engine.StartIntercept(browser, engine.InterceptSpec{BlockPatterns: patterns})
}
