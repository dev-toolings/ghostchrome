package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-rod/rod"
)

// SafeAreaInsets is what `env(safe-area-inset-*)` resolves to, in CSS pixels.
// A notched iPhone in a standalone PWA reports 59/34 (Dynamic Island, home
// indicator); every desktop Chromium reports 0, which hides the whole class of
// "fixed height plus inset padding" defects.
type SafeAreaInsets struct {
	Top    int `json:"top,omitempty"`
	Right  int `json:"right,omitempty"`
	Bottom int `json:"bottom,omitempty"`
	Left   int `json:"left,omitempty"`
}

// IsZero reports whether no inset is set; `omitzero` uses it.
func (s SafeAreaInsets) IsZero() bool {
	return s.Top == 0 && s.Right == 0 && s.Bottom == 0 && s.Left == 0
}

// Summary renders the four insets in CSS order (top/right/bottom/left).
func (s SafeAreaInsets) Summary() string {
	return fmt.Sprintf("safe-area %d/%d/%d/%d", s.Top, s.Right, s.Bottom, s.Left)
}

// Validate bounds every inset to what a device can plausibly report.
func (s SafeAreaInsets) Validate() error {
	for _, v := range []int{s.Top, s.Right, s.Bottom, s.Left} {
		if v < 0 || v > 500 {
			return fmt.Errorf("safe-area insets must be between 0 and 500 CSS pixels")
		}
	}
	return nil
}

// SafeAreaMethod names how the insets reached the page.
type SafeAreaMethod string

const (
	// SafeAreaMethodCDP is the native override (Emulation.setSafeAreaInsetsOverride),
	// which makes `env()` itself resolve to the insets.
	SafeAreaMethodCDP SafeAreaMethod = "cdp"
	// SafeAreaMethodCSS is the fallback for a Chromium without that command: every
	// same-origin style rule that references `env(safe-area-inset-*)` is copied
	// with the insets substituted and appended after its origin, in the same
	// cascade layer and conditional group, so the page lays out as on the device.
	SafeAreaMethodCSS SafeAreaMethod = "css-rewrite"
)

const safeAreaStyleID = "ghostchrome-safe-area"

// ApplySafeArea makes env(safe-area-inset-*) resolve to the given insets on
// the page and on every document it navigates to next. It returns the method
// that took effect.
func ApplySafeArea(page *rod.Page, insets SafeAreaInsets) (SafeAreaMethod, error) {
	if page == nil {
		return "", fmt.Errorf("page is nil")
	}
	if err := insets.Validate(); err != nil {
		return "", err
	}
	if err := callSafeAreaOverride(page, &insets); err == nil {
		// The native override also has to survive a stale CSS rewrite from a
		// previous fallback run on the same document.
		if err := installSafeAreaRewrite(page, nil); err != nil {
			return "", err
		}
		return SafeAreaMethodCDP, nil
	} else if !isUnsupportedCDPMethod(err) {
		return "", fmt.Errorf("safe-area override: %w", err)
	}
	if err := installSafeAreaRewrite(page, &insets); err != nil {
		return "", err
	}
	return SafeAreaMethodCSS, nil
}

// ClearSafeArea drops the insets: the native override when the browser has it,
// and the CSS rewrite in every case. Unsupported CDP is not an error here.
func ClearSafeArea(page *rod.Page) error {
	if page == nil {
		return nil
	}
	if err := callSafeAreaOverride(page, nil); err != nil && !isUnsupportedCDPMethod(err) {
		return fmt.Errorf("clear safe-area override: %w", err)
	}
	return installSafeAreaRewrite(page, nil)
}

// callSafeAreaOverride issues the raw CDP command; rod's generated proto
// predates it. A nil insets pointer sends an empty object, which per the CDP
// contract leaves every env() variable undefined again.
func callSafeAreaOverride(page *rod.Page, insets *SafeAreaInsets) error {
	params := map[string]any{"insets": map[string]int{}}
	if insets != nil {
		params["insets"] = map[string]int{
			"top":    insets.Top,
			"right":  insets.Right,
			"bottom": insets.Bottom,
			"left":   insets.Left,
		}
	}
	_, err := page.Call(page.GetContext(), string(page.SessionID), "Emulation.setSafeAreaInsetsOverride", params)
	return err
}

// isUnsupportedCDPMethod recognises Chrome's answer for a command its protocol
// version does not know ("'Emulation.setSafeAreaInsetsOverride' wasn't found").
func isUnsupportedCDPMethod(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "wasn't found") || strings.Contains(msg, "not found") || strings.Contains(msg, "unknown method")
}

// installSafeAreaRewrite registers the rewrite for every future document and
// runs it on the current one. A nil insets pointer removes the rewrite.
func installSafeAreaRewrite(page *rod.Page, insets *SafeAreaInsets) error {
	script, err := safeAreaRewriteScript(insets)
	if err != nil {
		return err
	}
	// EvalOnNewDocument wants a statement; rod's Eval wants a function it will
	// call itself, so the same body is invoked in the first case only.
	if _, err := page.EvalOnNewDocument(script + "();"); err != nil {
		return fmt.Errorf("safe-area init script: %w", err)
	}
	if _, err := page.Eval(script); err != nil {
		// A page with no document yet (about:blank before any navigation) is
		// not a failure: the init script covers the first real document.
		if !strings.Contains(strings.ToLower(err.Error()), "cannot find context") {
			return fmt.Errorf("safe-area rewrite: %w", err)
		}
	}
	return nil
}

// safeAreaRewriteScript builds the page-side rewrite. The script is
// idempotent per document and re-runs when a stylesheet is added later (HMR,
// lazy chunks). Only same-origin sheets are readable; a cross-origin sheet that
// uses env() stays untouched, which the caller cannot detect from here.
func safeAreaRewriteScript(insets *SafeAreaInsets) (string, error) {
	var config any
	if insets != nil {
		config = insets
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("safe-area config: %w", err)
	}
	return fmt.Sprintf(safeAreaRewriteTemplate, string(data), safeAreaStyleID), nil
}

// %[1]s: insets JSON or null; %[2]s: style element id. A bare arrow function:
// the installer decides whether to invoke it.
const safeAreaRewriteTemplate = `(() => {
	const insets = %[1]s;
	const STYLE_ID = %[2]q;
	const w = window;
	w.__ghostchromeSafeArea = insets;

	const head = (rule) => {
		if (rule instanceof CSSMediaRule) return "@media " + rule.conditionText;
		if (rule instanceof CSSSupportsRule) return "@supports " + rule.conditionText;
		if (typeof CSSContainerRule !== "undefined" && rule instanceof CSSContainerRule) return "@container " + rule.conditionText;
		if (typeof CSSLayerBlockRule !== "undefined" && rule instanceof CSSLayerBlockRule) return "@layer " + rule.name;
		return null;
	};

	const substitute = (css, name, px) => css
		.replace(new RegExp("env\\(safe-area-inset-" + name + "(?:\\s*,\\s*[^)]*)?\\)", "g"), px + "px")
		.replace(new RegExp("env\\(safe-area-max-inset-" + name + "(?:\\s*,\\s*[^)]*)?\\)", "g"), px + "px");

	const collect = () => {
		const out = [];
		const walk = (rule, ancestors) => {
			if (rule.cssRules && rule.cssRules.length && !(rule instanceof CSSStyleRule)) {
				const h = head(rule);
				if (h === null) return;
				for (const child of rule.cssRules) walk(child, ancestors.concat(h));
				return;
			}
			const text = rule.cssText;
			if (!text.includes("safe-area-")) return;
			out.push(ancestors.reduceRight((inner, h) => h + "{" + inner + "}", text));
		};
		for (const sheet of document.styleSheets) {
			if (sheet.ownerNode && sheet.ownerNode.id === STYLE_ID) continue; // fallback <style> only
			let rules;
			try { rules = sheet.cssRules; } catch (_) { continue; }
			for (const rule of rules) walk(rule, []);
		}
		return out;
	};

	// A constructed sheet adopted by the document sits after every document
	// stylesheet in the cascade and is not a DOM mutation, so it cannot fight a
	// framework that also keeps its own nodes last in <head> (a re-append loop
	// used to keep the page from ever settling under a dev server).
	// The sheet lives on window: a later install (new insets, or a clear) runs
	// in a fresh closure and must update the same sheet, not adopt a second one.
	const ensureSheet = () => {
		if (w.__ghostchromeSafeAreaSheet) return w.__ghostchromeSafeAreaSheet;
		if (typeof CSSStyleSheet === "undefined" || !("replaceSync" in CSSStyleSheet.prototype) || !("adoptedStyleSheets" in document)) return null;
		const sheet = new CSSStyleSheet();
		document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet];
		w.__ghostchromeSafeAreaSheet = sheet;
		return sheet;
	};
	const setCSS = (css) => {
		const target = ensureSheet();
		if (target) {
			target.replaceSync(css);
			return;
		}
		let fallbackStyle = document.getElementById(STYLE_ID);
		if (!fallbackStyle) {
			fallbackStyle = document.createElement("style");
			fallbackStyle.id = STYLE_ID;
			(document.head || document.documentElement).appendChild(fallbackStyle);
		}
		if (fallbackStyle.textContent !== css) fallbackStyle.textContent = css;
	};

	const apply = () => {
		const current = w.__ghostchromeSafeArea;
		if (!document.documentElement) return;
		if (!current) {
			setCSS("");
			return;
		}
		let css = collect().join("\n");
		css = substitute(css, "top", current.top || 0);
		css = substitute(css, "right", current.right || 0);
		css = substitute(css, "bottom", current.bottom || 0);
		css = substitute(css, "left", current.left || 0);
		setCSS(css);
	};

	if (w.__ghostchromeSafeAreaBound) {
		apply();
		return;
	}
	w.__ghostchromeSafeAreaBound = true;
	let timer = 0;
	const schedule = () => {
		clearTimeout(timer);
		timer = setTimeout(apply, 40);
	};
	const observe = () => {
		if (!document.documentElement) return;
		new MutationObserver((mutations) => {
			for (const m of mutations) {
				for (const node of m.addedNodes) {
					if (node.nodeType !== 1) continue;
					if (node.id === STYLE_ID) continue;
					const tag = node.tagName;
					if (tag === "STYLE" || tag === "LINK") {
						if (tag === "LINK") node.addEventListener("load", schedule, { once: true });
						schedule();
					}
				}
			}
		}).observe(document.documentElement, { childList: true, subtree: true });
	};
	if (document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", () => { observe(); apply(); }, { once: true });
	} else {
		observe();
		apply();
	}
	w.addEventListener("load", schedule, { once: true });
})`
