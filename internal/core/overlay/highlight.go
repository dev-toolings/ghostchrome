package overlay

import (
	"fmt"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
)

// HighlightTarget draws a persistent, pointer-transparent overlay around a
// target. The overlay belongs to the document, so it remains visible when a
// later CLI invocation reconnects to the same page.
func HighlightTarget(page *rod.Page, target, style string, snapshot *engine.PageSnapshot) error {
	el, err := engine.ResolveTarget(page, target, snapshot)
	if err != nil {
		return err
	}
	if _, err := el.Eval(highlightTargetScript, style); err != nil {
		return fmt.Errorf("draw highlight: %w", err)
	}
	return nil
}

// HideHighlights removes an overlay for target. When target is empty it
// removes every ghostchrome highlight in the current document.
func HideHighlights(page *rod.Page, target string, snapshot *engine.PageSnapshot) error {
	if target == "" {
		if _, err := page.Eval(hideAllHighlightsScript); err != nil {
			return fmt.Errorf("hide highlights: %w", err)
		}
		return nil
	}
	el, err := engine.ResolveTarget(page, target, snapshot)
	if err != nil {
		return err
	}
	if _, err := el.Eval(hideTargetHighlightScript); err != nil {
		return fmt.Errorf("hide highlight: %w", err)
	}
	return nil
}

const highlightTargetScript = `function(style) {
  const key = "__ghostchromeHighlightState";
  const state = window[key] || (window[key] = { items: [] });
  if (!state.update) {
    state.update = () => {
      state.items = state.items.filter(item => {
        if (!item.target || !item.target.isConnected || !item.overlay.isConnected) {
          item.overlay && item.overlay.remove();
          return false;
        }
        const rect = item.target.getBoundingClientRect();
        Object.assign(item.overlay.style, {
          left: rect.left + "px", top: rect.top + "px",
          width: rect.width + "px", height: rect.height + "px"
        });
        return true;
      });
    };
    addEventListener("resize", state.update, { passive: true });
    addEventListener("scroll", state.update, { passive: true, capture: true });
  }
  let item = state.items.find(item => item.target === this);
  if (!item) {
    const overlay = document.createElement("div");
    overlay.setAttribute("data-ghostchrome-highlight", "");
    Object.assign(overlay.style, {
      position: "fixed", pointerEvents: "none", zIndex: "2147483647",
      boxSizing: "border-box", border: "3px solid #ff2d55",
      borderRadius: "3px", boxShadow: "0 0 0 2px rgba(255,45,85,.2)"
    });
    (document.documentElement || document.body).appendChild(overlay);
    item = { target: this, overlay };
    state.items.push(item);
  }
  if (style) item.overlay.style.cssText += ";" + style;
  Object.assign(item.overlay.style, {
    position: "fixed", pointerEvents: "none", zIndex: "2147483647", boxSizing: "border-box"
  });
  state.update();
  return true;
}`

const hideTargetHighlightScript = `function() {
  const state = window.__ghostchromeHighlightState;
  if (!state) return 0;
  const before = state.items.length;
  state.items = state.items.filter(item => {
    if (item.target !== this) return true;
    item.overlay.remove();
    return false;
  });
  return before - state.items.length;
}`

const hideAllHighlightsScript = `() => {
  const state = window.__ghostchromeHighlightState;
  if (!state) return 0;
  for (const item of state.items) item.overlay.remove();
  state.items = [];
  return 0;
}`
