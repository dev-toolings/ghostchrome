package engine

import (
	"fmt"
	"math"
	mrand "math/rand/v2"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

// humanCfg is a package-level singleton consulted by ClickElement /
// TypeElement / HoverElement when the caller has opted in via SetHumanMode.
// Default (false) preserves byte-for-byte the legacy fast path.
var (
	humanCfg   bool
	humanCfgMu sync.RWMutex

	mouseMu        sync.Mutex
	lastMouseX     float64
	lastMouseY     float64
	lastMouseValid bool
)

// SetHumanMode toggles the human-input simulation globally. When false the
// engine uses the original Rod fast path; when true ClickElement /
// TypeElement / HoverElement dispatch human-shaped events.
func SetHumanMode(enabled bool) {
	humanCfgMu.Lock()
	humanCfg = enabled
	humanCfgMu.Unlock()
}

// HumanMode reports the current setting.
func HumanMode() bool {
	humanCfgMu.RLock()
	defer humanCfgMu.RUnlock()
	return humanCfg
}

func randFloat(lo, hi float64) float64 { return lo + mrand.Float64()*(hi-lo) }

func randIntRange(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + mrand.IntN(hi-lo)
}

func sleepRand(loMs, hiMs int) {
	time.Sleep(time.Duration(randIntRange(loMs, hiMs)) * time.Millisecond)
}

// elementCenter returns the on-page center of an element using the same
// content-quads shape Rod uses internally for clicks.
func elementCenter(el *rod.Element) (float64, float64, error) {
	shape, err := el.Shape()
	if err != nil {
		return 0, 0, err
	}
	pt := shape.OnePointInside()
	if pt == nil {
		box := shape.Box()
		if box == nil {
			return 0, 0, fmt.Errorf("element has no visible shape")
		}
		return box.X + box.Width/2, box.Y + box.Height/2, nil
	}
	return pt.X, pt.Y, nil
}

// humanMove dispatches a Bézier-shaped sequence of mouseMoved CDP events
// from the last known cursor position (or the target itself the first time)
// to (toX, toY). Updates the package-level cursor state.
func humanMove(page *rod.Page, toX, toY float64) error {
	mouseMu.Lock()
	fromX, fromY := lastMouseX, lastMouseY
	if !lastMouseValid {
		// First move: start from a plausible offset so we don't jump from (0,0).
		fromX = toX + randFloat(-150, 150)
		fromY = toY + randFloat(-150, 150)
	}
	mouseMu.Unlock()

	dx := toX - fromX
	dy := toY - fromY
	dist := math.Hypot(dx, dy)

	jitter := 0.15 * dist
	if jitter < 4 {
		jitter = 4
	}
	c1x := fromX + dx*0.33 + randFloat(-jitter, jitter)
	c1y := fromY + dy*0.33 + randFloat(-jitter, jitter)
	c2x := fromX + dx*0.66 + randFloat(-jitter, jitter)
	c2y := fromY + dy*0.66 + randFloat(-jitter, jitter)

	steps := int(math.Round(dist / 8))
	if steps < 12 {
		steps = 12
	}
	if steps > 60 {
		steps = 60
	}

	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		mt := 1 - t
		x := mt*mt*mt*fromX + 3*mt*mt*t*c1x + 3*mt*t*t*c2x + t*t*t*toX
		y := mt*mt*mt*fromY + 3*mt*mt*t*c1y + 3*mt*t*t*c2y + t*t*t*toY

		ev := &proto.InputDispatchMouseEvent{
			Type:        proto.InputDispatchMouseEventTypeMouseMoved,
			X:           x,
			Y:           y,
			PointerType: proto.InputDispatchMouseEventPointerTypeMouse,
		}
		if err := ev.Call(page); err != nil {
			return err
		}
		sleepRand(8, 18)
	}

	mouseMu.Lock()
	lastMouseX = toX
	lastMouseY = toY
	lastMouseValid = true
	mouseMu.Unlock()
	return nil
}

// humanHover scrolls then performs a Bézier mouse move to the element center.
func humanHover(page *rod.Page, el *rod.Element) error {
	if err := el.ScrollIntoView(); err != nil {
		return fmt.Errorf("scroll: %w", err)
	}
	cx, cy, err := elementCenter(el)
	if err != nil {
		return fmt.Errorf("center: %w", err)
	}
	return humanMove(page, cx, cy)
}

// humanClick moves to a small random offset around the element center, holds
// briefly, then dispatches mousePressed / mouseReleased.
func humanClick(page *rod.Page, el *rod.Element, button proto.InputMouseButton) error {
	if err := el.ScrollIntoView(); err != nil {
		return fmt.Errorf("scroll: %w", err)
	}
	cx, cy, err := elementCenter(el)
	if err != nil {
		return fmt.Errorf("center: %w", err)
	}
	tx := cx + randFloat(-3, 3)
	ty := cy + randFloat(-3, 3)

	if err := humanMove(page, tx, ty); err != nil {
		return fmt.Errorf("move: %w", err)
	}
	sleepRand(40, 120)

	press := &proto.InputDispatchMouseEvent{
		Type:        proto.InputDispatchMouseEventTypeMousePressed,
		X:           tx,
		Y:           ty,
		Button:      button,
		ClickCount:  1,
		PointerType: proto.InputDispatchMouseEventPointerTypeMouse,
	}
	if err := press.Call(page); err != nil {
		return fmt.Errorf("mousedown: %w", err)
	}
	sleepRand(50, 130)
	release := &proto.InputDispatchMouseEvent{
		Type:        proto.InputDispatchMouseEventTypeMouseReleased,
		X:           tx,
		Y:           ty,
		Button:      button,
		ClickCount:  1,
		PointerType: proto.InputDispatchMouseEventPointerTypeMouse,
	}
	if err := release.Call(page); err != nil {
		return fmt.Errorf("mouseup: %w", err)
	}
	return nil
}

// qwertyNeighbors lists plausible typo neighbors for a-z (US QWERTY).
var qwertyNeighbors = map[rune]string{
	'a': "qwsz", 'b': "vghn", 'c': "xdfv", 'd': "serfcx",
	'e': "wsdr", 'f': "drtgvc", 'g': "ftyhbv", 'h': "gyujnb",
	'i': "ujko", 'j': "huikmn", 'k': "jiolm", 'l': "kop",
	'm': "njk", 'n': "bhjm", 'o': "iklp", 'p': "ol",
	'q': "wa", 'r': "edft", 's': "awedxz", 't': "rfgy",
	'u': "yhji", 'v': "cfgb", 'w': "qase", 'x': "zsdc",
	'y': "tghu", 'z': "asx",
}

func neighborOf(r rune) (rune, bool) {
	low := unicode.ToLower(r)
	pool, ok := qwertyNeighbors[low]
	if !ok || pool == "" {
		return 0, false
	}
	pick := rune(pool[mrand.IntN(len(pool))])
	if unicode.IsUpper(r) {
		pick = unicode.ToUpper(pick)
	}
	return pick, true
}

// humanType inserts text one rune at a time with realistic delays and a
// small typo rate. The element must already be focused by the caller.
func humanType(page *rod.Page, text string) error {
	prev := rune(0)
	for _, r := range text {
		// Base delay; shorter for repeated runs, longer after sentence punctuation.
		delay := randIntRange(40, 140)
		if r == prev {
			delay = randIntRange(30, 90)
		}

		// 2% chance of a transient typo on alphabetic chars.
		if unicode.IsLetter(r) && mrand.Float64() < 0.02 {
			if wrong, ok := neighborOf(r); ok {
				if err := page.Keyboard.Type(input.Key(wrong)); err != nil {
					return fmt.Errorf("type typo: %w", err)
				}
				sleepRand(80, 180)
				if err := page.Keyboard.Type(input.Backspace); err != nil {
					return fmt.Errorf("type backspace: %w", err)
				}
				sleepRand(40, 110)
			}
		}

		if err := page.Keyboard.Type(input.Key(r)); err != nil {
			return fmt.Errorf("type rune: %w", err)
		}
		time.Sleep(time.Duration(delay) * time.Millisecond)
		if strings.ContainsRune(".,;!?", r) {
			sleepRand(150, 280)
		}
		prev = r
	}
	return nil
}
