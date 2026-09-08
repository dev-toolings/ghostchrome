package feedback

import (
	"strings"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
)

// Observation is the structured feedback packet returned after each agent op.
// All fields are omitempty so an empty observation serialises to `{}`.
type Observation struct {
	// ConsoleErrors holds runtime errors or uncaught exceptions observed during
	// the op (kind=error from the Observer).
	ConsoleErrors []engine.ObserverEvent `json:"console_errors,omitempty"`

	// NetworkFailed holds network requests that failed (4xx/5xx or transport
	// error) during the op.
	NetworkFailed []engine.ObserverEvent `json:"network_failed,omitempty"`

	// A11yDiff is the compact text diff of the a11y tree between before and
	// after the op (FormatDiff output). Empty when there is no change.
	A11yDiff string `json:"a11y_diff,omitempty"`

	// Dialog is the text of a pending JS dialog detected on the page ("alert
	// open: <message>"). Empty when no dialog is open.
	Dialog string `json:"dialog,omitempty"`

	// CaptchaHint is set when a bot-challenge page is detected
	// ("datadome", "cloudflare", or a generic "captcha").
	CaptchaHint string `json:"captcha_hint,omitempty"`

	// URL is the current page URL at the time the observation was built.
	URL string `json:"url,omitempty"`
}

// BuildObservation constructs an Observation from the observer events that
// occurred during a single op, the a11y snapshots taken before and after, and
// the current page state.
//
// before may be nil (first op, no prior snapshot). after may be nil if the op
// failed before extraction. events is the slice from Observer.Drain().
func BuildObservation(page *rod.Page, before, after *engine.PageSnapshot, events []engine.ObserverEvent) Observation {
	return BuildObservationOpts(page, before, after, events, false)
}

// BuildObservationOpts is BuildObservation with an explicit captcha-scan flag.
// scanCaptcha=false skips page.HTML() so warm clicks do not pay a full document dump.
func BuildObservationOpts(page *rod.Page, before, after *engine.PageSnapshot, events []engine.ObserverEvent, scanCaptcha bool) Observation {
	obs := Observation{}

	// Prefer the snapshot URL so a warm click does not pay Page.getFrameTree.
	if after != nil && after.URL != "" {
		obs.URL = after.URL
	} else if before != nil && before.URL != "" {
		obs.URL = before.URL
	} else if info, err := page.Info(); err == nil && info != nil {
		obs.URL = info.URL
	}

	// Classify observer events.
	for _, evt := range events {
		switch {
		case evt.Kind == engine.KindError:
			obs.ConsoleErrors = append(obs.ConsoleErrors, evt)
		case evt.Kind == engine.KindNet && (evt.Failed != "" || evt.Status >= 400):
			obs.NetworkFailed = append(obs.NetworkFailed, evt)
		}
	}

	// A11y diff: compare snapshots if both are present and distinct.
	if before != nil && after != nil && len(before.Refs) > 0 && len(after.Refs) > 0 {
		d := engine.DiffRefs(before.Refs, after.Refs)
		if !d.Unchanged {
			obs.A11yDiff = engine.FormatDiff(d)
		}
	}

	// Captcha detection (cheap — single Info()+HTML() check).
	if scanCaptcha {
		obs.CaptchaHint = detectCaptchaHint(page)
	}

	return obs
}

// detectCaptchaHint returns a short label when the page appears to be a
// bot-challenge page, or empty string otherwise.
func detectCaptchaHint(page *rod.Page) string {
	info, err := page.Info()
	if err != nil {
		return ""
	}

	if strings.Contains(info.URL, "captcha-delivery.com") ||
		strings.Contains(info.URL, "geo.captcha-delivery.com") {
		return "datadome"
	}
	if strings.Contains(info.Title, "Just a moment") ||
		strings.Contains(info.Title, "Attention Required") {
		return "cloudflare"
	}

	html, err := page.HTML()
	if err != nil {
		return ""
	}

	switch {
	case strings.Contains(html, "captcha-delivery.com") ||
		strings.Contains(html, "ct.captcha-delivery.com") ||
		strings.Contains(html, "geo.captcha-delivery.com"):
		return "datadome"
	case strings.Contains(html, "cdn-cgi/challenge-platform") ||
		strings.Contains(html, "challenges.cloudflare.com") ||
		strings.Contains(html, "cf-turnstile") ||
		strings.Contains(html, "_cf_chl_opt"):
		return "cloudflare"
	case strings.Contains(html, "recaptcha") || strings.Contains(html, "hcaptcha"):
		return "captcha"
	}
	return ""
}
