package antibot

import (
	"strings"
	"time"

	"github.com/go-rod/rod"
)

// Common cookie consent button patterns (text content matching)
var cookieAcceptPatterns = []string{
	"tout accepter",
	"accepter tout",
	"accept all",
	"accepter",
	"j'accepte",
	"ok",
	"agree",
	"consent",
	"allow all",
	"autoriser",
}

// Common cookie banner selectors. Order matters — vendor-specific IDs come
// first because they're unambiguous; generic [id*=cookie] patterns last.
var cookieBannerSelectors = []string{
	// OneTrust
	"#onetrust-accept-btn-handler",
	"#accept-recommended-btn-handler",
	// Cookiebot
	"#CybotCookiebotDialogBodyLevelButtonAccept",
	"#CybotCookiebotDialogBodyButtonAccept",
	// Didomi
	"#didomi-notice-agree-button",
	"button.didomi-button-highlight",
	// Quantcast Choice
	"button.qc-cmp2-summary-buttons[mode=primary]",
	".qc-cmp2-buttons-desktop button:last-child",
	// ConsentManager.net (autoscout24, leboncoin, ...)
	"button.cmpboxbtn.cmpboxbtnyes",
	"button[data-cmp-action=accept]",
	"button.cmpboxbtnyes",
	"#cmpwelcomebtnyes",
	// Axeptio
	"#axeptio_btn_acceptAll",
	"button.ax-button[data-test=button-accept-all]",
	// TrustArc / TRUSTe
	"#truste-consent-button",
	".trustarc-banner-overlay button.call",
	// Generic CMP IDs
	"#sp_message_iframe_*",
	".js-cookie-accept",
	".cc-accept",
	".cc-allow",
	// Generic last-resort patterns
	"[id*='cookie'] button",
	"[class*='cookie'] button",
	"[id*='consent'] button",
	"[class*='consent'] button",
	"[id*='gdpr'] button",
	"[class*='gdpr'] button",
	"[aria-label*='cookie'] button",
	"[data-testid*='cookie'] button",
}

// DismissCookieBanner attempts to find and click a cookie accept button.
// Returns true if a banner was found and dismissed.
func DismissCookieBanner(page *rod.Page) bool {
	deadline := time.Now().Add(2 * time.Second)
	for {
		if dismissCookieBannerOnce(page) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func dismissCookieBannerOnce(page *rod.Page) bool {
	// Strategy 1: Find buttons by text content
	for _, pattern := range cookieAcceptPatterns {
		dismissed := tryClickByText(page, pattern)
		if dismissed {
			_ = page.WaitStable(300 * time.Millisecond)
			return true
		}
	}

	// Strategy 2: Find buttons in cookie-related containers
	for _, sel := range cookieBannerSelectors {
		elements, err := page.Elements(sel)
		if err != nil || len(elements) == 0 {
			continue
		}
		for _, el := range elements {
			visible, _ := el.Visible()
			if !visible {
				continue
			}
			text, _ := el.Text()
			textLower := strings.ToLower(strings.TrimSpace(text))
			for _, pattern := range cookieAcceptPatterns {
				if strings.Contains(textLower, pattern) {
					_ = el.Click("left", 1)
					_ = page.WaitStable(300 * time.Millisecond)
					return true
				}
			}
		}
	}

	return false
}

// tryClickByText finds a visible button/link containing the given text and clicks it.
func tryClickByText(page *rod.Page, text string) bool {
	// Use XPath to find buttons/links containing the text (case-insensitive)
	xpath := `//button[contains(translate(., 'ABCDEFGHIJKLMNOPQRSTUVWXYZÀÂÄÉÈÊËÏÎÔÙÛÜÇ', 'abcdefghijklmnopqrstuvwxyzàâäéèêëïîôùûüç'), '` + text + `')] | //a[contains(translate(., 'ABCDEFGHIJKLMNOPQRSTUVWXYZÀÂÄÉÈÊËÏÎÔÙÛÜÇ', 'abcdefghijklmnopqrstuvwxyzàâäéèêëïîôùûüç'), '` + text + `')]`

	elements, err := page.ElementsX(xpath)
	if err != nil || len(elements) == 0 {
		return false
	}

	for _, el := range elements {
		visible, _ := el.Visible()
		if !visible {
			continue
		}
		_ = el.Click("left", 1)
		return true
	}

	return false
}
