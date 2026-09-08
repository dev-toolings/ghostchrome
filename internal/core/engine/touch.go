package engine

import (
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// Touch gestures.
//
// The mouse-based helpers (click, DragDrop) synthesize Input.dispatchMouseEvent.
// A mobile web app that listens for touchstart/touchmove/touchend — a drawer
// swipe, a carousel, a pull-to-refresh — never sees those, so a "drag" on a
// phone-emulated page silently does nothing. These helpers dispatch the real
// Input.dispatchTouchEvent sequence instead.
//
// Chrome only routes synthesized touch events to a page whose widget accepts
// touch, which is what Emulation.setTouchEmulationEnabled turns on. Callers
// must therefore emulate a touch device first; EnsureTouchEmulation is the
// idempotent way to do it.
//
// Do NOT reach for Emulation.setEmitTouchEventsForMouse here, however tempting
// "a click should also fire touchstart" sounds. With it enabled, Chrome routes
// mouse input through the gesture pipeline and never acknowledges
// Input.dispatchMouseEvent: every click hangs forever, which froze the whole
// MCP server behind its lock. Playwright and Puppeteer do not use it either.
// A page that needs a real finger gets SwipeTouch or TapTouch.

// defaultSwipeSteps is the number of intermediate touchMove events emitted for
// a swipe. Enough samples for a JS gesture handler to compute a velocity, few
// enough to stay cheap over CDP.
const defaultSwipeSteps = 12

// defaultSwipeDuration is how long a swipe takes when the caller gives no
// duration. Roughly a deliberate human flick.
const defaultSwipeDuration = 300 * time.Millisecond

// SetTouchEmulation toggles Chrome's touch input emulation on a page. It is the
// exported form of the toggle ApplyDevice uses for touch device presets.
func SetTouchEmulation(page *rod.Page, enabled bool) error {
	return setTouchEmulation(page, enabled)
}

// EnsureTouchEmulation turns on touch input emulation so synthesized touch
// events reach the page. Idempotent: calling it on an already touch-emulated
// page is a no-op from the page's point of view.
func EnsureTouchEmulation(page *rod.Page) error {
	return SetTouchEmulation(page, true)
}

// SwipeTouch performs a single-finger swipe from (fromX, fromY) to (toX, toY)
// in CSS pixels relative to the viewport: touchStart, `steps` touchMove events
// spread over `duration`, then touchEnd.
//
// Requires touch emulation (see EnsureTouchEmulation); without it Chrome drops
// the events and the page sees nothing.
func SwipeTouch(page *rod.Page, fromX, fromY, toX, toY float64, steps int, duration time.Duration) error {
	if page == nil {
		return fmt.Errorf("swipe: no page")
	}
	if steps <= 0 {
		steps = defaultSwipeSteps
	}
	if duration <= 0 {
		duration = defaultSwipeDuration
	}

	if err := dispatchTouch(page, proto.InputDispatchTouchEventTypeTouchStart, fromX, fromY); err != nil {
		return fmt.Errorf("touch start: %w", err)
	}

	interval := duration / time.Duration(steps)
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		x := fromX + (toX-fromX)*t
		y := fromY + (toY-fromY)*t
		if err := dispatchTouch(page, proto.InputDispatchTouchEventTypeTouchMove, x, y); err != nil {
			// Cancel the in-flight gesture so the page is not left with a
			// finger stuck down.
			_ = dispatchTouchEnd(page, proto.InputDispatchTouchEventTypeTouchCancel)
			return fmt.Errorf("touch move %d/%d: %w", i, steps, err)
		}
		if interval > 0 {
			time.Sleep(interval)
		}
	}

	if err := dispatchTouchEnd(page, proto.InputDispatchTouchEventTypeTouchEnd); err != nil {
		return fmt.Errorf("touch end: %w", err)
	}
	settleAfterAction(page, 0)
	return nil
}

// TapTouch dispatches a touchStart/touchEnd pair at one point — the gesture a
// mobile page sees when a finger taps it. Use it when a control only reacts to
// touch events; ordinary clicks remain the default for everything else.
func TapTouch(page *rod.Page, x, y float64) error {
	if page == nil {
		return fmt.Errorf("tap: no page")
	}
	if err := dispatchTouch(page, proto.InputDispatchTouchEventTypeTouchStart, x, y); err != nil {
		return fmt.Errorf("touch start: %w", err)
	}
	if err := dispatchTouchEnd(page, proto.InputDispatchTouchEventTypeTouchEnd); err != nil {
		return fmt.Errorf("touch end: %w", err)
	}
	settleAfterAction(page, 0)
	return nil
}

// dispatchTouch sends one touch event carrying a single active point.
func dispatchTouch(page *rod.Page, kind proto.InputDispatchTouchEventType, x, y float64) error {
	id := 0.0
	force := 1.0
	radius := 1.0
	return proto.InputDispatchTouchEvent{
		Type: kind,
		TouchPoints: []*proto.InputTouchPoint{{
			X:       x,
			Y:       y,
			ID:      &id,
			Force:   &force,
			RadiusX: &radius,
			RadiusY: &radius,
		}},
	}.Call(page)
}

// dispatchTouchEnd sends a terminating touch event. Per the CDP contract,
// touchEnd and touchCancel must carry no touch points at all.
func dispatchTouchEnd(page *rod.Page, kind proto.InputDispatchTouchEventType) error {
	return proto.InputDispatchTouchEvent{
		Type:        kind,
		TouchPoints: []*proto.InputTouchPoint{},
	}.Call(page)
}
