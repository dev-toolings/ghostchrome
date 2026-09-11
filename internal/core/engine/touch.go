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
// Each event carries an explicit CDP timestamp on the requested schedule, so the
// page reads the speed the caller asked for. A round trip costs tens of
// milliseconds, which a short gesture cannot outrun: without the timestamps a
// 90 ms flick reached the page as a 480 ms drag and no velocity threshold ever
// fired. Pacing is also computed against a fixed start rather than by sleeping
// a full interval per step, so the round trips stop accumulating.
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

	origin := time.Now()
	stamp := func(offset time.Duration) proto.TimeSinceEpoch {
		return proto.TimeSinceEpoch(float64(origin.Add(offset).UnixNano()) / 1e9)
	}

	if err := dispatchTouch(page, proto.InputDispatchTouchEventTypeTouchStart, fromX, fromY, stamp(0)); err != nil {
		return fmt.Errorf("touch start: %w", err)
	}

	interval := duration / time.Duration(steps)
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		x := fromX + (toX-fromX)*t
		y := fromY + (toY-fromY)*t
		offset := time.Duration(float64(duration) * t)
		if err := dispatchTouch(page, proto.InputDispatchTouchEventTypeTouchMove, x, y, stamp(offset)); err != nil {
			// Cancel the in-flight gesture so the page is not left with a
			// finger stuck down.
			_ = dispatchTouchEnd(page, proto.InputDispatchTouchEventTypeTouchCancel, stamp(offset))
			return fmt.Errorf("touch move %d/%d: %w", i, steps, err)
		}
		// Wall-clock pacing still matters for anything the page does between
		// events (a rAF, a transition), but only for the time not already spent
		// in the round trip.
		if remaining := time.Until(origin.Add(offset)); remaining > 0 && interval > 0 {
			time.Sleep(remaining)
		}
	}

	if err := dispatchTouchEnd(page, proto.InputDispatchTouchEventTypeTouchEnd, stamp(duration)); err != nil {
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
	now := proto.TimeSinceEpoch(float64(time.Now().UnixNano()) / 1e9)
	if err := dispatchTouch(page, proto.InputDispatchTouchEventTypeTouchStart, x, y, now); err != nil {
		return fmt.Errorf("touch start: %w", err)
	}
	if err := dispatchTouchEnd(page, proto.InputDispatchTouchEventTypeTouchEnd, now); err != nil {
		return fmt.Errorf("touch end: %w", err)
	}
	settleAfterAction(page, 0)
	return nil
}

// dispatchTouch sends one touch event carrying a single active point.
func dispatchTouch(page *rod.Page, kind proto.InputDispatchTouchEventType, x, y float64, at proto.TimeSinceEpoch) error {
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
		Timestamp: at,
	}.Call(page)
}

// dispatchTouchEnd sends a terminating touch event. Per the CDP contract,
// touchEnd and touchCancel must carry no touch points at all.
func dispatchTouchEnd(page *rod.Page, kind proto.InputDispatchTouchEventType, at proto.TimeSinceEpoch) error {
	return proto.InputDispatchTouchEvent{
		Type:        kind,
		TouchPoints: []*proto.InputTouchPoint{},
		Timestamp:   at,
	}.Call(page)
}
