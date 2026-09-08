package engine

import (
	"encoding/json"
	"testing"
	"time"
)

// touchProbeDocument records every touch event the page receives so a test can
// assert that a synthesized gesture really reached the DOM, not just CDP.
const touchProbeDocument = `<!doctype html>
<html>
  <head><meta name="viewport" content="width=device-width, initial-scale=1"></head>
  <body style="margin:0;height:2000px">
    <div id="surface" style="width:100%;height:900px;background:#eee"></div>
    <script>
      window.__touch = {start: 0, move: 0, end: 0, startY: null, lastY: null};
      addEventListener('touchstart', e => {
        window.__touch.start++;
        window.__touch.startY = e.touches[0].clientY;
      }, {passive: true});
      addEventListener('touchmove', e => {
        window.__touch.move++;
        window.__touch.lastY = e.touches[0].clientY;
      }, {passive: true});
      addEventListener('touchend', () => { window.__touch.end++; }, {passive: true});
    </script>
  </body>
</html>`

type touchProbe struct {
	Start  int      `json:"start"`
	Move   int      `json:"move"`
	End    int      `json:"end"`
	StartY *float64 `json:"startY"`
	LastY  *float64 `json:"lastY"`
}

// TestSwipeTouchDispatchesRealTouchEvents is the regression test for the reason
// swipe exists at all: DragDrop synthesizes mouse events, which a touch-only
// handler — a mobile drawer, a carousel — never sees.
func TestSwipeTouchDispatchesRealTouchEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	device, ok := DeviceByName("iphone-14-pro-max")
	if !ok {
		t.Fatal("iphone-14-pro-max preset missing")
	}
	if err := ApplyDevice(page, device); err != nil {
		t.Fatalf("apply device: %v", err)
	}
	if _, err := Navigate(page, dataURL(touchProbeDocument), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	if err := SwipeTouch(page, 215, 700, 215, 200, 10, 150*time.Millisecond); err != nil {
		t.Fatalf("swipe: %v", err)
	}

	raw, err := EvalJS(page, `JSON.stringify(window.__touch)`, "", nil)
	if err != nil {
		t.Fatalf("eval touch probe: %v", err)
	}
	var probe touchProbe
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		t.Fatalf("decode touch probe %q: %v", raw, err)
	}
	if probe.Start != 1 {
		t.Fatalf("touchstart count = %d, want 1", probe.Start)
	}
	// Chrome coalesces touchmove events on the compositor, so the page sees at
	// most one per dispatched step and sometimes fewer. Assert the gesture
	// shape, not an exact count, or the test flakes.
	if probe.Move < 1 || probe.Move > 10 {
		t.Fatalf("touchmove count = %d, want between 1 and 10", probe.Move)
	}
	if probe.End != 1 {
		t.Fatalf("touchend count = %d, want 1", probe.End)
	}
	if probe.StartY == nil || probe.LastY == nil {
		t.Fatalf("touch coordinates missing: %s", raw)
	}
	if *probe.StartY <= *probe.LastY {
		t.Fatalf("swipe did not move upward: startY=%v lastY=%v", *probe.StartY, *probe.LastY)
	}
}

// clickProbeDocument has a button that only mutates the DOM and a button that
// navigates, so one test can cover a click with and without a navigation.
const clickProbeDocument = `<!doctype html>
<html>
  <head><meta name="viewport" content="width=device-width, initial-scale=1"></head>
  <body style="margin:0">
    <button id="plain" onclick="document.title = 'clicked'">Plain</button>
    <button id="nav" onclick="setTimeout(() => { location.hash = '#signed-in'; document.title = 'navigated' }, 20)">Se connecter</button>
  </body>
</html>`

// TestClickUnderActiveEmulationDoesNotHang is the regression test for the MCP
// freeze: with Emulation.setEmitTouchEventsForMouse enabled, Chrome converts
// mouse input into gestures and never acknowledges Input.dispatchMouseEvent, so
// the first click after an `emulate` call blocked forever while holding the MCP
// server lock — every later tool call queued behind it.
//
// Both a plain click and a click that navigates must return well inside the
// test deadline while a phone profile is active.
func TestClickUnderActiveEmulationDoesNotHang(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	device, ok := DeviceByName("iphone-14-pro-max")
	if !ok {
		t.Fatal("iphone-14-pro-max preset missing")
	}
	if err := ApplyEmulationProfile(page, EmulationFromDevice(device)); err != nil {
		t.Fatalf("apply emulation profile: %v", err)
	}
	if _, err := Navigate(page, dataURL(clickProbeDocument), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	for _, tc := range []struct {
		selector string
		want     string
	}{
		{"#plain", "clicked"},
		{"#nav", "navigated"},
	} {
		el, err := page.Element(tc.selector)
		if err != nil {
			t.Fatalf("resolve %s: %v", tc.selector, err)
		}
		done := make(chan error, 1)
		go func() { done <- ClickElement(page, el) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("click %s: %v", tc.selector, err)
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("click %s never returned under active emulation", tc.selector)
		}
		// The navigating button acts on a timer, like a real login that posts
		// before its router pushes. Poll for the effect instead of assuming the
		// click handler already ran.
		deadline := time.Now().Add(5 * time.Second)
		var title string
		for {
			title, err = EvalJS(page, `document.title`, "", nil)
			if err != nil {
				t.Fatalf("eval title after %s: %v", tc.selector, err)
			}
			if title == tc.want || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if title != tc.want {
			t.Fatalf("title after clicking %s = %s, want %s", tc.selector, title, tc.want)
		}
	}
}

// TestStealthKeepsEmulatedTouchPoints guards the second half of the phone-shell
// bug: ApplyStealth used to pin navigator.maxTouchPoints to 0 on every new
// document, so a --stealth MCP server emulating an iPhone reported
// `pointer: coarse` with zero touch points. Mobile web apps feature-detect on
// maxTouchPoints, so the phone shell stayed off.
func TestStealthKeepsEmulatedTouchPoints(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	if err := ApplyStealth(page); err != nil {
		t.Fatalf("apply stealth: %v", err)
	}
	device, ok := DeviceByName("iphone-14-pro-max")
	if !ok {
		t.Fatal("iphone-14-pro-max preset missing")
	}
	if err := ApplyEmulationProfile(page, EmulationFromDevice(device)); err != nil {
		t.Fatalf("apply emulation profile: %v", err)
	}
	// The stealth patches install on new documents, so the assertion only means
	// anything after a navigation.
	if _, err := Navigate(page, dataURL(touchProbeDocument), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	points, err := EvalJS(page, `navigator.maxTouchPoints`, "", nil)
	if err != nil {
		t.Fatalf("eval maxTouchPoints: %v", err)
	}
	if points == "0" {
		t.Fatal("maxTouchPoints is 0 under stealth while a touch device is emulated")
	}
	coarse, err := EvalJS(page, `matchMedia('(pointer: coarse)').matches`, "", nil)
	if err != nil {
		t.Fatalf("eval pointer coarse: %v", err)
	}
	if coarse != "true" {
		t.Fatalf("pointer:coarse under stealth = %s, want true", coarse)
	}
}

// TestApplyEmulationProfileTurnsTouchOff guards the difference between
// ApplyEmulationProfile and the additive ApplyEmulationState: switching back to
// a desktop profile must really restore pointer:fine.
func TestApplyEmulationProfileTurnsTouchOff(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	phone := EmulationState{Width: 430, Height: 932, DPR: 3, Mobile: true, Touch: true}
	if err := ApplyEmulationProfile(page, phone); err != nil {
		t.Fatalf("apply phone profile: %v", err)
	}
	if _, err := Navigate(page, dataURL(touchProbeDocument), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	coarse, err := EvalJS(page, `matchMedia('(pointer: coarse)').matches`, "", nil)
	if err != nil {
		t.Fatalf("eval pointer coarse: %v", err)
	}
	if coarse != "true" {
		t.Fatalf("pointer:coarse on phone profile = %s, want true", coarse)
	}

	desktop := EmulationState{Width: 1920, Height: 1080, DPR: 1}
	if err := ApplyEmulationProfile(page, desktop); err != nil {
		t.Fatalf("apply desktop profile: %v", err)
	}
	points, err := EvalJS(page, `navigator.maxTouchPoints`, "", nil)
	if err != nil {
		t.Fatalf("eval maxTouchPoints: %v", err)
	}
	if points != "0" {
		t.Fatalf("maxTouchPoints after desktop profile = %s, want 0", points)
	}
}
