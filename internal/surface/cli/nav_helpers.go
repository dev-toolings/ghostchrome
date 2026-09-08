package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
)

// navChallengeRecovered records whether the most recent navigateIfRequested
// call detected AND cleared a bot challenge (DataDome/Cloudflare
// interstitial). Extraction helpers read it to opt into the SSR fallback
// (includeSSR) on the recovery path: a page that just survived a challenge is
// exactly the case where the caller needs the data — the a11y tree may still
// be sparse right after the reload, and DataDome/Cloudflare-protected sites
// are overwhelmingly SSR-rendered (Next.js) to begin with. Always overwritten
// (never just set-if-true) so a later clean navigate correctly resets it.
//
// One-shot: each CLI invocation is its own process, so this package var never
// outlives a single command — but callers still consume it via
// consumeNavChallengeRecovered so the "one recovered navigate -> one SSR
// extract" contract matches the agent (JSONL) session's semantics exactly.
var navChallengeRecovered bool

// consumeNavChallengeRecovered returns the current flag and resets it to
// false, mirroring runtime.Session.consumeChallengeRecovered.
func consumeNavChallengeRecovered() bool {
	v := navChallengeRecovered
	navChallengeRecovered = false
	return v
}

func navigateIfRequested(page *rod.Page, targetURL string, waitStrategy string) *engine.PageInfo {
	if targetURL == "" {
		return nil
	}

	applyStealthIfNeeded(page)
	info, err := engine.Navigate(page, targetURL, waitStrategy)
	if err != nil {
		exitErr("navigate", err)
	}
	navChallengeRecovered = waitForChallengeIfStealth(page, info)
	dismissCookiesIfNeeded(page)
	waitForSelectorOrSleep(page)
	return info
}

func waitForChallengeIfStealth(page *rod.Page, info *engine.PageInfo) bool {
	if !flagStealth || info == nil {
		return false
	}
	budget := time.Duration(flagTimeout) * time.Second * 4 / 5
	if budget > 110*time.Second {
		budget = 110 * time.Second
	}
	if budget < 45*time.Second {
		budget = 45 * time.Second
	}
	return engine.WaitForBotChallenge(page, budget)
}

func waitForSelectorOrSleep(page *rod.Page) {
	if flagWaitSelector != "" {
		scoped := page.Timeout(time.Duration(flagTimeout) * time.Second)
		el, err := scoped.Element(flagWaitSelector)
		if err != nil || el == nil {
			fmt.Fprintf(os.Stderr, "wait-selector %q not found: %v\n", flagWaitSelector, err)
		} else if err := el.WaitVisible(); err != nil {
			fmt.Fprintf(os.Stderr, "wait-selector %q never became visible: %v\n", flagWaitSelector, err)
		}
	}
	if flagWaitMs > 0 {
		time.Sleep(time.Duration(flagWaitMs) * time.Millisecond)
	}
}
