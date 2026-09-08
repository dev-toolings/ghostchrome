package overlay

import (
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/core/coretest"
	"github.com/dev-toolings/ghostchrome/internal/core/engine"
)

func TestHighlightTargetPersistsAndHides(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := coretest.NewIsolatedPage(t)
	if _, err := engine.Navigate(page, coretest.DataURL(`<!doctype html><button id="target">Target</button><button id="second">Second</button>`), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	if err := HighlightTarget(page, "#target", "border-color: rgb(1, 2, 3)", nil); err != nil {
		t.Fatalf("highlight: %v", err)
	}
	result, err := page.Eval(`() => ({ count: document.querySelectorAll('[data-ghostchrome-highlight]').length, color: document.querySelector('[data-ghostchrome-highlight]').style.borderColor })`)
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	got := result.Value.Val().(map[string]interface{})
	if got["count"] != float64(1) || got["color"] != "rgb(1, 2, 3)" {
		t.Fatalf("unexpected overlay: %#v", got)
	}

	if err := HideHighlights(page, "#target", nil); err != nil {
		t.Fatalf("hide target: %v", err)
	}
	result, err = page.Eval(`() => document.querySelectorAll('[data-ghostchrome-highlight]').length`)
	if err != nil || result.Value.Int() != 0 {
		t.Fatalf("overlay remains after hide: result=%v err=%v", result, err)
	}

	if err := HighlightTarget(page, "#target", "", nil); err != nil {
		t.Fatalf("highlight target: %v", err)
	}
	if err := HighlightTarget(page, "#second", "", nil); err != nil {
		t.Fatalf("highlight second: %v", err)
	}
	if err := HideHighlights(page, "", nil); err != nil {
		t.Fatalf("hide all: %v", err)
	}
	result, err = page.Eval(`() => document.querySelectorAll('[data-ghostchrome-highlight]').length`)
	if err != nil || result.Value.Int() != 0 {
		t.Fatalf("overlays remain after hide all: result=%v err=%v", result, err)
	}
}
