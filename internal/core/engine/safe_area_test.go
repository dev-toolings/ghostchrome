package engine

import (
	"testing"
)

// safeAreaTestDocument mirrors the shapes a PWA shell uses: an unlayered rule,
// a Tailwind-style layered utility, a coarse-pointer media block, and a
// fallback value inside env(). Every one must resolve to the emulated inset.
const safeAreaTestDocument = `<!doctype html>
<html>
  <head>
    <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
    <style>
      @layer base, utilities;
      .bar { height: calc(40px + env(safe-area-inset-top)); }
      @layer utilities { .pb-safe { padding-bottom: env(safe-area-inset-bottom, 8px); } }
      @media (min-width: 1px) { .nav { margin-left: env(safe-area-inset-left); } }
    </style>
  </head>
  <body><div class="bar"></div><div class="pb-safe"></div><div class="nav"></div></body>
</html>`

func TestSafeAreaInsetsStateAndSummary(t *testing.T) {
	if !(SafeAreaInsets{}).IsZero() {
		t.Fatal("zero insets should be zero")
	}
	if (EmulationState{SafeArea: SafeAreaInsets{Top: 59}}).Empty() {
		t.Fatal("a safe-area inset is replayable, so the state is not empty")
	}
	got := EmulationState{Width: 430, Height: 932, DPR: 3, SafeArea: SafeAreaInsets{Top: 59, Bottom: 34}}.Summary()
	if want := "430x932@3x safe-area 59/0/34/0"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	if err := (SafeAreaInsets{Top: 501}).Validate(); err == nil {
		t.Fatal("an inset above 500px must be rejected")
	}
	if err := (SafeAreaInsets{Left: -1}).Validate(); err == nil {
		t.Fatal("a negative inset must be rejected")
	}
}

// TestApplySafeAreaResolvesEnvInsets proves the page lays out as on the device
// whichever path the running Chromium supports: the native CDP override or the
// stylesheet rewrite. Both must survive a navigation and clear on demand.
func TestApplySafeAreaResolvesEnvInsets(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	if _, err := Navigate(page, dataURL(safeAreaTestDocument), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	method, err := ApplySafeArea(page, SafeAreaInsets{Top: 59, Bottom: 34, Left: 7})
	if err != nil {
		t.Fatalf("apply safe area: %v", err)
	}
	if method != SafeAreaMethodCDP && method != SafeAreaMethodCSS {
		t.Fatalf("unexpected method %q", method)
	}

	const probe = `JSON.stringify({
		bar: getComputedStyle(document.querySelector('.bar')).height,
		pb: getComputedStyle(document.querySelector('.pb-safe')).paddingBottom,
		nav: getComputedStyle(document.querySelector('.nav')).marginLeft,
	})`
	assert := func(step, want string) {
		t.Helper()
		got, err := EvalJS(page, probe, "", nil)
		if err != nil {
			t.Fatalf("%s: eval: %v", step, err)
		}
		if got != want {
			t.Fatalf("%s (%s): computed = %s, want %s", step, method, got, want)
		}
	}
	applied := `{"bar":"99px","pb":"34px","nav":"7px"}`
	assert("current document", applied)

	// A fresh document must come up with the insets already in force.
	if _, err := Navigate(page, dataURL(safeAreaTestDocument), "load"); err != nil {
		t.Fatalf("navigate again: %v", err)
	}
	assert("next document", applied)

	if err := ClearSafeArea(page); err != nil {
		t.Fatalf("clear safe area: %v", err)
	}
	// Chromium defines every safe-area-inset-* as 0, so the 8px fallback inside
	// env() is never taken once the override is gone.
	cleared := `{"bar":"40px","pb":"0px","nav":"0px"}`
	assert("after clear", cleared)
	if _, err := Navigate(page, dataURL(safeAreaTestDocument), "load"); err != nil {
		t.Fatalf("navigate after clear: %v", err)
	}
	assert("next document after clear", cleared)
}
