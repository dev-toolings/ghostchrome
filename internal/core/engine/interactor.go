package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

// ErrStaleRef indicates that a ref no longer maps to a live element.
var ErrStaleRef = errors.New("stale ref: snapshot is missing or no longer matches the page")

func parseRef(ref string) (string, error) {
	trimmed := strings.TrimPrefix(InternalRef(ref), "@")
	idx, err := strconv.Atoi(trimmed)
	if err != nil || idx < 1 {
		return "", fmt.Errorf("invalid ref %q: must be @N where N >= 1", ref)
	}
	return "@" + trimmed, nil
}

// ResolveRef finds an element by its ref (@1, @2, etc.) using a persisted snapshot.
func ResolveRef(page *rod.Page, ref string, snapshot *PageSnapshot) (*rod.Element, error) {
	parsed, err := parseRef(ref)
	if err != nil {
		return nil, err
	}
	return ResolveRefSemantic(page, parsed, snapshot)
}

// ClickRef clicks the element at the given ref.
func ClickRef(page *rod.Page, ref string, snapshot *PageSnapshot) error {
	return ClickRefWithButton(page, ref, snapshot, proto.InputMouseButtonLeft)
}

// ClickRefWithButton clicks the element at ref with an explicit mouse button.
func ClickRefWithButton(page *rod.Page, ref string, snapshot *PageSnapshot, button proto.InputMouseButton) error {
	return withSemanticRetry(page, ref, snapshot, func(el *rod.Element) error {
		return ClickElementWithButton(page, el, button)
	})
}

// ClickElement performs a click on an already-resolved element (used by the
// locator path so the same scroll+click+wait logic is shared).
func ClickElement(page *rod.Page, el *rod.Element) error {
	return ClickElementWithButton(page, el, proto.InputMouseButtonLeft)
}

// ClickElementWithButton performs a click with an explicit mouse button.
func ClickElementWithButton(page *rod.Page, el *rod.Element, button proto.InputMouseButton) error {
	if err := EnsureActionable(el, actionBudget(page)); err != nil {
		return fmt.Errorf("click: %w", err)
	}
	if HumanMode() {
		if err := humanClick(page, el, button); err != nil {
			return fmt.Errorf("click: %w", err)
		}
		settleAfterAction(page, 0)
		WaitForClickNavigation(page)
		WaitForClickDownload(page)
		return nil
	}
	if err := clickActionableElement(page, el, button); err != nil {
		return fmt.Errorf("click: %w", err)
	}
	settleAfterAction(page, 0)
	WaitForClickNavigation(page)
	WaitForClickDownload(page)
	return nil
}

// clickActionableElement uses DOM activation for navigational links inside a
// child frame. Rod's coordinate translation for nested frames is unreliable
// with the bundled Chromium on macOS ARM, while HTMLElement.click preserves
// the link's normal navigation semantics. Other controls keep a real input
// event, including file inputs and non-left mouse buttons.
func clickActionableElement(page *rod.Page, el *rod.Element, button proto.InputMouseButton) error {
	elPage := el.Page()
	if button == proto.InputMouseButtonLeft && page != nil && elPage != nil &&
		elPage.FrameID != "" && elPage.FrameID != page.FrameID && ClickNavHint(elPage) {
		_, err := el.Eval(`() => this.click()`)
		return err
	}
	return el.Click(button, 1)
}

// DblClickRef double-clicks the element at the given ref.
func DblClickRef(page *rod.Page, ref string, snapshot *PageSnapshot) error {
	return DblClickRefWithButton(page, ref, snapshot, proto.InputMouseButtonLeft)
}

// DblClickRefWithButton double-clicks an element with an explicit mouse button.
func DblClickRefWithButton(page *rod.Page, ref string, snapshot *PageSnapshot, button proto.InputMouseButton) error {
	return withSemanticRetry(page, ref, snapshot, func(el *rod.Element) error {
		return dblClickElementWithButton(page, el, button)
	})
}

func dblClickElementWithButton(page *rod.Page, el *rod.Element, button proto.InputMouseButton) error {
	if err := EnsureActionable(el, actionBudget(page)); err != nil {
		return fmt.Errorf("dblclick: %w", err)
	}
	if HumanMode() {
		if err := humanClick(page, el, button); err != nil {
			return fmt.Errorf("dblclick: %w", err)
		}
		sleepRand(40, 80)
		if err := humanClick(page, el, button); err != nil {
			return fmt.Errorf("dblclick: %w", err)
		}
		settleAfterAction(page, 0)
		return nil
	}
	if err := el.Click(button, 2); err != nil {
		return fmt.Errorf("dblclick: %w", err)
	}
	settleAfterAction(page, 0)
	return nil
}

// SetCheckedRef brings a checkbox/radio to the desired checked state. It is
// idempotent: if the element is already in the target state it does nothing,
// so "check" never accidentally toggles a box that was already ticked.
func SetCheckedRef(page *rod.Page, ref string, checked bool, snapshot *PageSnapshot) error {
	return withSemanticRetry(page, ref, snapshot, func(el *rod.Element) error {
		prop, err := el.Property("checked")
		if err != nil {
			return fmt.Errorf("read checked state: %w", err)
		}
		if prop.Bool() == checked {
			return nil // already in the desired state
		}
		return ClickElement(page, el)
	})
}

// HasSelector reports whether at least one element matches selector.
// It is a non-throwing alternative to page.Element used by polling loops.
func HasSelector(page *rod.Page, selector string) bool {
	_, err := page.Element(selector)
	return err == nil
}

// CountSelector returns the number of elements matching selector. Errors are
// swallowed and reported as 0 so the polling caller can distinguish
// "not-yet-rendered" from "match count below threshold".
func CountSelector(page *rod.Page, selector string) int {
	elements, err := page.Elements(selector)
	if err != nil {
		return 0
	}
	return len(elements)
}

// ScrollToRef scrolls the element at the given ref into view without
// performing any other interaction.
func ScrollToRef(page *rod.Page, ref string, snapshot *PageSnapshot) error {
	el, err := ResolveRef(page, ref, snapshot)
	if err != nil {
		return err
	}
	if err := el.ScrollIntoView(); err != nil {
		return fmt.Errorf("scroll into view: %w", err)
	}
	settleAfterAction(page, 0)
	return nil
}

// ScrollToY scrolls the page to an absolute Y pixel position. When
// bottomSentinel is true, the page is scrolled to document.body.scrollHeight
// regardless of the y argument — use this for "scroll-to bottom".
// Returns the final window.scrollY as observed after the scroll.
func ScrollToY(page *rod.Page, y int, bottomSentinel bool) (int, error) {
	script := fmt.Sprintf(`() => { window.scrollTo(0, %d); return Math.round(window.scrollY); }`, y)
	if bottomSentinel {
		script = `() => { window.scrollTo(0, document.body.scrollHeight); return Math.round(window.scrollY); }`
	}
	res, err := page.Eval(script)
	if err != nil {
		return 0, fmt.Errorf("scroll eval: %w", err)
	}
	settleAfterAction(page, 0)
	return int(res.Value.Num()), nil
}

// ScrollBy scrolls by a relative Y offset. Returns the final scrollY.
func ScrollBy(page *rod.Page, dy int) (int, error) {
	script := fmt.Sprintf(`() => { window.scrollBy(0, %d); return Math.round(window.scrollY); }`, dy)
	res, err := page.Eval(script)
	if err != nil {
		return 0, fmt.Errorf("scroll-by eval: %w", err)
	}
	settleAfterAction(page, 0)
	return int(res.Value.Num()), nil
}

// UploadRef sets the files on a file-input element.
//
// The target can be identified either by:
//   - ref: a @N reference from the current snapshot (works when the input is
//     a native, visible <input type=file>).
//   - selector: a CSS selector (use this when the visible widget is a styled
//     button wrapping a hidden input — common pattern).
//
// Exactly one of ref or selector must be non-empty.
func UploadRef(page *rod.Page, ref string, selector string, files []string, snapshot *PageSnapshot) error {
	if err := ActivePolicy.AllowAction("upload"); err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("upload: need at least one file path")
	}
	for _, p := range files {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("upload: file %q: %w", p, err)
		}
	}

	var el *rod.Element
	var err error
	switch {
	case selector != "":
		el, err = page.Element(selector)
		if err != nil {
			return fmt.Errorf("selector %q: %w", selector, err)
		}
	case ref != "":
		return withSemanticRetry(page, ref, snapshot, func(el *rod.Element) error {
			if err := el.SetFiles(files); err != nil {
				return fmt.Errorf("set files: %w", err)
			}
			settleAfterAction(page, 0)
			return nil
		})
	default:
		return fmt.Errorf("upload: need either a ref or --selector")
	}

	if err := el.SetFiles(files); err != nil {
		return fmt.Errorf("set files: %w", err)
	}
	settleAfterAction(page, 0)
	return nil
}

// FillFields types each ref→value pair in numeric @N order. After every field
// it rebuilds the snapshot so a country→city cascade cannot click a stale ref.
func FillFields(b *Browser, page *rod.Page, fields map[string]string, snapshot *PageSnapshot) (int, *PageSnapshot, error) {
	if len(fields) == 0 {
		return 0, snapshot, fmt.Errorf("fill: fields required")
	}
	refs := make([]string, 0, len(fields))
	for ref := range fields {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		ni, iok := snapshotRefNum(refs[i])
		nj, jok := snapshotRefNum(refs[j])
		if iok && jok {
			return ni < nj
		}
		if iok != jok {
			return iok
		}
		return refs[i] < refs[j]
	})
	snap := snapshot
	for _, ref := range refs {
		if err := TypeRef(page, ref, fields[ref], snap); err != nil {
			return 0, snap, fmt.Errorf("fill %s: %w", ref, err)
		}
		if b != nil {
			_ = b.InvalidateCachedExtract(page)
		}
		result, err := Extract(page, LevelSkeleton, "", false)
		if err == nil {
			if b != nil {
				_ = b.SaveSnapshot(page, result)
			}
			if next, serr := BuildSnapshot(page, result); serr == nil {
				snap = next
			}
		}
	}
	return len(fields), snap, nil
}

// TypeRef types text into the element at the given ref.
// Uses focus + select all + keyboard typing to work with React/Vue/Angular.
func TypeRef(page *rod.Page, ref string, text string, snapshot *PageSnapshot) error {
	return withSemanticRetry(page, ref, snapshot, func(el *rod.Element) error {
		return TypeElement(page, el, text)
	})
}

// TypeElement writes text into an already-resolved element.
func TypeElement(page *rod.Page, el *rod.Element, text string) error {
	if err := EnsureActionable(el, actionBudget(page)); err != nil {
		return fmt.Errorf("type: %w", err)
	}
	if HumanMode() {
		if err := humanClick(page, el, proto.InputMouseButtonLeft); err != nil {
			return fmt.Errorf("focus-click: %w", err)
		}
		if err := el.Focus(); err != nil {
			return fmt.Errorf("focus: %w", err)
		}
		_ = el.SelectAllText()
		sleepRand(60, 140)
		if err := humanType(page, text); err != nil {
			return err
		}
		_ = el.Blur()
		return nil
	}
	if err := el.Focus(); err != nil {
		return fmt.Errorf("focus: %w", err)
	}
	_ = el.Click(proto.InputMouseButtonLeft, 3)
	_ = el.SelectAllText()
	if err := page.InsertText(text); err != nil {
		return fmt.Errorf("type text: %w", err)
	}
	_ = el.Blur()
	return nil
}

// TypeFocused writes text into the currently focused element without changing
// focus or clearing existing content.
func TypeFocused(page *rod.Page, text string) error {
	if HumanMode() {
		if err := humanType(page, text); err != nil {
			return err
		}
		return nil
	}
	if err := page.InsertText(text); err != nil {
		return fmt.Errorf("type focused text: %w", err)
	}
	return nil
}

// SubmitOnElement focuses el and presses Enter on it. TypeElement blurs the
// field after writing (so change/validation handlers fire), which means a
// follow-up Enter would otherwise land on document.body — re-focus first so
// the keydown reaches the input and submits its form.
func SubmitOnElement(page *rod.Page, el *rod.Element) error {
	if err := el.Focus(); err != nil {
		return fmt.Errorf("focus before submit: %w", err)
	}
	if err := page.Keyboard.Type(input.Enter); err != nil {
		return fmt.Errorf("submit: %w", err)
	}
	settleAfterAction(page, 0)
	setClickNavHint(page, true)
	WaitForClickNavigation(page)
	return nil
}

// TakeScreenshot captures the page or a specific element.
// If elementRef is non-empty, captures only that element.
// If fullPage is true, captures the full scrollable page.
// quality controls JPEG/WebP quality (1-100); PNG is used if quality <= 0.
func TakeScreenshot(page *rod.Page, fullPage bool, elementRef string, quality int, snapshot *PageSnapshot) ([]byte, error) {
	return TakeScreenshotScaled(page, fullPage, elementRef, quality, 0, snapshot)
}

// TakeScreenshotScaled is like TakeScreenshot but accepts a device scale
// factor. scale <= 0 or == 1 means native resolution. scale = 0.5 halves both
// dimensions (quartering pixel count and file size).
func TakeScreenshotScaled(page *rod.Page, fullPage bool, elementRef string, quality int, scale float64, snapshot *PageSnapshot) ([]byte, error) {
	return takeScreenshotImpl(page, fullPage, elementRef, "", quality, scale, snapshot)
}

// TakeScreenshotFormat is the explicit-format variant. format is "png", "jpeg",
// "webp", or "" to default (webp when quality > 0, png otherwise). WebP at
// q=60 is typically 30-50% smaller than JPEG at the same visual quality —
// preferred when the consumer is an LLM agent that pays per byte.
func TakeScreenshotFormat(page *rod.Page, fullPage bool, elementRef string, format string, quality int, snapshot *PageSnapshot) ([]byte, error) {
	return takeScreenshotImpl(page, fullPage, elementRef, format, quality, 0, snapshot)
}

func takeScreenshotImpl(page *rod.Page, fullPage bool, elementRef string, format string, quality int, scale float64, snapshot *PageSnapshot) ([]byte, error) {
	fmtChoice := resolveScreenshotFormat(format, quality)

	if scale <= 0 {
		scale = 1
	}

	if elementRef != "" {
		el, err := ResolveTarget(page, elementRef, snapshot)
		if err != nil {
			return nil, err
		}
		// Element screenshots only support PNG via Rod's helper. Fall back
		// to a manual capture for WebP/JPEG.
		if fmtChoice == proto.PageCaptureScreenshotFormatPng {
			return el.Screenshot(proto.PageCaptureScreenshotFormatPng, 0)
		}
		return elementScreenshotFmt(el, fmtChoice, quality)
	}

	req := &proto.PageCaptureScreenshot{Format: fmtChoice}
	if quality > 0 && fmtChoice != proto.PageCaptureScreenshotFormatPng {
		req.Quality = intPtr(quality)
	}

	if fullPage {
		metrics, err := proto.PageGetLayoutMetrics{}.Call(page)
		if err != nil {
			return nil, fmt.Errorf("get layout metrics: %w", err)
		}
		req.Clip = &proto.PageViewport{
			X: 0, Y: 0,
			Width:  metrics.ContentSize.Width,
			Height: metrics.ContentSize.Height,
			Scale:  scale,
		}
		req.CaptureBeyondViewport = true
	}

	data, err := req.Call(page)
	if err != nil {
		return nil, fmt.Errorf("screenshot: %w", err)
	}
	return data.Data, nil
}

// resolveScreenshotFormat picks the CDP format from a user hint + quality.
// Empty hint + quality > 0 → webp (smallest for LLM-targeted captures).
// Empty hint + quality == 0 → png (lossless default).
func resolveScreenshotFormat(hint string, quality int) proto.PageCaptureScreenshotFormat {
	switch strings.ToLower(hint) {
	case "png":
		return proto.PageCaptureScreenshotFormatPng
	case "jpeg", "jpg":
		return proto.PageCaptureScreenshotFormatJpeg
	case "webp":
		return proto.PageCaptureScreenshotFormatWebp
	}
	if quality > 0 {
		return proto.PageCaptureScreenshotFormatWebp
	}
	return proto.PageCaptureScreenshotFormatPng
}

// elementScreenshotFmt captures a single element with an explicit format.
// Computes the element clip from the box model and reuses Page.captureScreenshot.
func elementScreenshotFmt(el *rod.Element, fmt_ proto.PageCaptureScreenshotFormat, quality int) ([]byte, error) {
	box, err := el.Shape()
	if err != nil {
		return nil, fmt.Errorf("element shape: %w", err)
	}
	rect := box.Box()
	if rect == nil || rect.Width <= 0 || rect.Height <= 0 {
		return nil, fmt.Errorf("element has no visible box")
	}
	req := &proto.PageCaptureScreenshot{
		Format: fmt_,
		Clip: &proto.PageViewport{
			X: rect.X, Y: rect.Y,
			Width: rect.Width, Height: rect.Height,
			Scale: 1,
		},
		CaptureBeyondViewport: true,
	}
	if quality > 0 && fmt_ != proto.PageCaptureScreenshotFormatPng {
		req.Quality = intPtr(quality)
	}
	data, err := req.Call(el.Page())
	if err != nil {
		return nil, fmt.Errorf("element screenshot: %w", err)
	}
	return data.Data, nil
}

// EvalJS evaluates JavaScript on the page or in an element context.
// If elementRef is non-empty, the JS runs with `this` bound to that element.
func EvalJS(page *rod.Page, expr string, elementRef string, snapshot *PageSnapshot) (string, error) {
	if err := ActivePolicy.AllowAction("eval"); err != nil {
		return "", err
	}
	if elementRef != "" {
		el, err := ResolveTarget(page, elementRef, snapshot)
		if err != nil {
			return "", err
		}
		wrapped := fmt.Sprintf("function(){ return %s }", expr)
		res, err := el.Eval(wrapped)
		if err != nil {
			return "", fmt.Errorf("eval on element: %w", err)
		}
		return formatEvalResult(res), nil
	}

	// Wrap as async arrow function to support await. Wrap the body in parens
	// so that expressions starting with a leading comment don't trigger ASI
	// after `return` (e.g. scripts loaded from --script files). Trim trailing
	// whitespace/semicolons so a script body that ends with `})();` still
	// fits inside the parenthesised expression context.
	body := strings.TrimRight(expr, " \t\r\n;")
	wrapped := fmt.Sprintf("async () => { return (\n%s\n); }", body)
	res, err := page.Eval(wrapped)
	if err != nil {
		return "", fmt.Errorf("eval: %w", err)
	}
	return formatEvalResult(res), nil
}

// EvalJSTimeout runs EvalJS with a per-call deadline. When the page's main
// thread is busy (anti-bot fingerprinting, infinite analytics loop, ...),
// the underlying CDP Runtime.evaluate call hangs until the deadline fires
// instead of blocking the server-wide timeout. Returns a typed timeout
// error the caller can surface clearly to the LLM.
func EvalJSTimeout(page *rod.Page, expr string, elementRef string, snapshot *PageSnapshot, d time.Duration) (string, error) {
	if d <= 0 {
		return EvalJS(page, expr, elementRef, snapshot)
	}
	ctx, cancel := context.WithTimeout(page.GetContext(), d)
	defer cancel()
	return EvalJS(page.Context(ctx), expr, elementRef, snapshot)
}

// formatEvalResult converts a rod eval result to a string.
func formatEvalResult(res *proto.RuntimeRemoteObject) string {
	if res == nil {
		return "undefined"
	}
	val := res.Value.Val()
	if val == nil {
		return "undefined"
	}
	return fmt.Sprintf("%v", val)
}

// intPtr converts an int to *int for proto fields.
func intPtr(v int) *int {
	return &v
}

// ---------------------------------------------------------------------------
// Additional interaction functions
// ---------------------------------------------------------------------------

// SelectOption selects option(s) in a <select> element by visible text.
func SelectOption(page *rod.Page, ref string, values []string, snapshot *PageSnapshot) error {
	return withSemanticRetry(page, ref, snapshot, func(el *rod.Element) error {
		if err := el.Select(values, true, rod.SelectorTypeText); err != nil {
			return fmt.Errorf("select: %w", err)
		}
		settleAfterAction(page, 0)
		return nil
	})
}

// HoverRef hovers over the element at the given ref.
func HoverRef(page *rod.Page, ref string, snapshot *PageSnapshot) error {
	return withSemanticRetry(page, ref, snapshot, func(el *rod.Element) error {
		return HoverElement(page, el)
	})
}

// HoverElement hovers on an already-resolved element.
func HoverElement(page *rod.Page, el *rod.Element) error {
	if err := EnsureActionable(el, actionBudget(page)); err != nil {
		return fmt.Errorf("hover: %w", err)
	}
	if HumanMode() {
		if err := humanHover(page, el); err != nil {
			return fmt.Errorf("hover: %w", err)
		}
		settleAfterAction(page, 0)
		return nil
	}
	if err := el.ScrollIntoView(); err != nil {
		return fmt.Errorf("scroll: %w", err)
	}
	if err := el.Hover(); err != nil {
		return fmt.Errorf("hover: %w", err)
	}
	settleAfterAction(page, 0)
	return nil
}

// keyMap maps human-friendly key names to Rod input keys.
var keyMap = map[string]input.Key{
	"enter": input.Enter, "tab": input.Tab, "escape": input.Escape,
	"backspace": input.Backspace, "delete": input.Delete,
	"arrowup": input.ArrowUp, "arrowdown": input.ArrowDown,
	"arrowleft": input.ArrowLeft, "arrowright": input.ArrowRight,
	"space": input.Space, "home": input.Home, "end": input.End,
	"pageup": input.PageUp, "pagedown": input.PageDown,
	"f1": input.F1, "f2": input.F2, "f3": input.F3, "f4": input.F4,
	"f5": input.F5, "f6": input.F6, "f7": input.F7, "f8": input.F8,
	"f9": input.F9, "f10": input.F10, "f11": input.F11, "f12": input.F12,
}

// PressKey sends a keyboard key press. If ref is non-empty, focuses the element first.
func PressKey(page *rod.Page, key string, ref string, snapshot *PageSnapshot) error {
	if ref != "" {
		err := withSemanticRetry(page, ref, snapshot, func(el *rod.Element) error {
			if err := el.Focus(); err != nil {
				return fmt.Errorf("focus: %w", err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	k, ok := keyMap[strings.ToLower(key)]
	if !ok {
		return fmt.Errorf("unknown key %q — supported: enter, tab, escape, backspace, delete, arrowup, arrowdown, arrowleft, arrowright, space, home, end, pageup, pagedown", key)
	}
	err := page.Keyboard.Type(k)
	if err != nil {
		return fmt.Errorf("press: %w", err)
	}
	switch {
	case pressKeyMaySubmit(key):
		setClickNavHint(page, true)
		WaitForClickNavigation(page)
	case pressKeyMayNavigate(key):
		settleAfterAction(page, 0)
	}
	return nil
}

func pressKeyMaySubmit(key string) bool {
	switch strings.ToLower(key) {
	case "enter", "numpadenter":
		return true
	}
	return false
}

func pressKeyMayNavigate(key string) bool {
	switch strings.ToLower(key) {
	case "enter", "space", "numpadenter":
		return true
	}
	return false
}

// WaitForSelector waits for a CSS selector to appear in the DOM.
func WaitForSelector(page *rod.Page, selector string, timeoutSec int) error {
	p := page.Timeout(time.Duration(timeoutSec) * time.Second)
	_, err := p.Element(selector)
	if err != nil {
		return fmt.Errorf("timeout waiting for %q after %ds", selector, timeoutSec)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Tab management
// ---------------------------------------------------------------------------

// TabInfo holds metadata about a browser tab.
type TabInfo struct {
	Index    int    `json:"index"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	TargetID string `json:"target_id,omitempty"`
	Active   bool   `json:"active,omitempty"`
}

// ListTabs returns info for every open tab in the browser.
func ListTabs(browser *rod.Browser, currentTargetID string) ([]TabInfo, error) {
	pages, err := browser.Timeout(250 * time.Millisecond).Pages()
	if err != nil {
		return nil, err
	}
	var tabs []TabInfo
	for i, p := range pages {
		info, _ := p.Info()
		title := ""
		url := ""
		if info != nil {
			title = info.Title
			url = info.URL
		}
		targetID := string(p.TargetID)
		tabs = append(tabs, TabInfo{
			Index:    i,
			URL:      url,
			Title:    title,
			TargetID: targetID,
			Active:   currentTargetID != "" && currentTargetID == targetID,
		})
	}
	return tabs, nil
}

// RefMayOpenPopup reports whether clicking this snapshot ref is likely to
// open a new tab (anchor with target=_blank / window.open / download).
func RefMayOpenPopup(snapshot *PageSnapshot, ref string) bool {
	if snapshot == nil {
		return false
	}
	parsed, err := parseRef(ref)
	if err != nil {
		return false
	}
	info, ok := snapshot.Refs[parsed]
	if !ok {
		return false
	}
	role := strings.ToLower(info.Role)
	href := strings.ToLower(info.Href)
	name := strings.ToLower(info.Name)
	return refLooksLikePopup(role, href, name)
}

func refLooksLikePopup(role, href, name string) bool {
	href = strings.ToLower(strings.TrimSpace(href))
	name = strings.ToLower(name)
	role = strings.ToLower(role)
	if strings.Contains(href, "javascript:window.open") || strings.Contains(href, "window.open(") {
		return true
	}
	if strings.Contains(name, "opens in a new") || strings.Contains(name, "open in new") {
		return true
	}
	// AX url/name never includes target=_blank. Only scan tabs for explicit
	// popup/download signals so ordinary same-tab links skip Pages().
	if role == "link" {
		switch {
		case strings.HasPrefix(href, "javascript:window.open"):
			return true
		case strings.Contains(href, "target=_blank"), strings.Contains(href, "target='_blank'"), strings.Contains(href, "target=\"_blank\""):
			return true
		}
	}
	return false
}

// SnapshotPageTargets records open page targets so a later PopupOpenedSince
// can detect a target=_blank / window.open without waiting.
func SnapshotPageTargets(page *rod.Page) map[string]struct{} {
	out := map[string]struct{}{}
	if page == nil {
		return out
	}
	pages, err := page.Browser().Pages()
	if err != nil {
		return out
	}
	for _, p := range pages {
		out[string(p.TargetID)] = struct{}{}
	}
	return out
}

// PopupOpenedSince returns a page that appeared after SnapshotPageTargets.
func PopupOpenedSince(page *rod.Page, before map[string]struct{}) *rod.Page {
	if page == nil {
		return nil
	}
	pages, err := page.Browser().Pages()
	if err != nil {
		return nil
	}
	for _, p := range pages {
		if _, ok := before[string(p.TargetID)]; !ok {
			return p
		}
	}
	return nil
}

// SwitchTab activates the tab at the given index and returns its page.
func SwitchTab(browser *rod.Browser, index int) (*rod.Page, error) {
	pages, err := browser.Pages()
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(pages) {
		return nil, fmt.Errorf("tab index %d out of range (0-%d)", index, len(pages)-1)
	}
	_, err = pages[index].Activate()
	if err != nil {
		return nil, err
	}
	return pages[index], nil
}

// NewTab opens a new tab, optionally navigating to url (blank when empty),
// activates it, and returns the page.
func NewTab(browser *rod.Browser, url string) (*rod.Page, error) {
	if url != "" {
		if err := ActivePolicy.AllowURL(url); err != nil {
			return nil, err
		}
	}
	page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, err
	}
	if err := ApplyInitScripts(page); err != nil {
		forgetPageEventState(page)
		_ = page.Close()
		return nil, err
	}
	if url != "" && url != "about:blank" {
		if err := page.Navigate(url); err != nil {
			forgetPageEventState(page)
			_ = page.Close()
			return nil, err
		}
	}
	if _, err := page.Activate(); err != nil {
		forgetPageEventState(page)
		_ = page.Close()
		return nil, err
	}
	return page, nil
}

// CloseTab closes the tab at the given index.
func CloseTab(browser *rod.Browser, index int) (proto.TargetTargetID, error) {
	pages, err := browser.Pages()
	if err != nil {
		return "", err
	}
	if index < 0 || index >= len(pages) {
		return "", fmt.Errorf("tab index %d out of range (0-%d)", index, len(pages)-1)
	}
	page := pages[index]
	targetID := page.TargetID
	// Drop per-target listener registry state before closing the target so a
	// long-lived daemon does not retain the closed tab's identity.
	forgetPageEventState(page)
	return targetID, page.Close()
}

// ---------------------------------------------------------------------------
// Viewport & dialogs
// ---------------------------------------------------------------------------

// MobileViewportThreshold is the width below which SetViewport switches Chrome
// to mobile emulation. Callers persisting the resulting profile must use the
// same threshold or the replayed viewport would differ from the applied one.
const MobileViewportThreshold = 768

// SetViewport overrides the page viewport dimensions.
func SetViewport(page *rod.Page, width, height int) error {
	return proto.EmulationSetDeviceMetricsOverride{
		Width:             width,
		Height:            height,
		DeviceScaleFactor: 1,
		Mobile:            width < MobileViewportThreshold,
	}.Call(page)
}

// DialogAutoPolicy is mutated in place so JSONL/MCP can change accept/dismiss
// without restarting the CDP listener. Use Set and Snapshot when accessing it
// after the handler has started; the handler runs concurrently with callers.
type DialogAutoPolicy struct {
	mu     sync.RWMutex
	Accept bool
	Prompt string
}

// Set updates the policy atomically from the dialog handler's point of view.
func (p *DialogAutoPolicy) Set(accept bool, prompt string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.Accept = accept
	p.Prompt = prompt
	p.mu.Unlock()
}

// Snapshot returns a consistent policy value for one dialog event.
func (p *DialogAutoPolicy) Snapshot() (accept bool, prompt string) {
	if p == nil {
		return true, ""
	}
	p.mu.RLock()
	accept, prompt = p.Accept, p.Prompt
	p.mu.RUnlock()
	return accept, prompt
}

// StartDialogAutoHandler accepts or dismisses JavaScript dialogs as they open
// so a click that triggers alert/confirm cannot stall the agent loop.
var (
	dialogAutoOnce     sync.Map // *rod.Page -> struct{}
	dialogAutoPolicies sync.Map // *rod.Page -> *DialogAutoPolicy
	dialogAutoWaiters  sync.Map // *rod.Page -> *dialogWaiter
)

type dialogWaiter struct {
	accept bool
	prompt string
	done   chan dialogOutcome
}

type dialogOutcome struct {
	result *DialogResult
	err    error
}

func StartDialogAutoHandler(page *rod.Page, policy *DialogAutoPolicy) {
	if page == nil || policy == nil {
		return
	}
	// Navigation can reset Page domain state. Re-enable it on every call while
	// keeping exactly one event listener for each rod.Page instance.
	_ = proto.PageEnable{}.Call(page)
	dialogAutoPolicies.Store(page, policy)
	if _, loaded := dialogAutoOnce.LoadOrStore(page, struct{}{}); loaded {
		return
	}
	StartFileChooserIntercept(page)
	// Keep one persistent event subscription. Re-arming HandleDialog after
	// each event races with back-to-back alert() calls (the second dialog can
	// open before the next WaitEvent is registered). EachEvent stays subscribed
	// for the page lifetime and does not disable Page.enable after handling.
	go page.EachEvent(func(event *proto.PageJavascriptDialogOpening) {
		// An explicit HandleNextDialog call takes precedence over the default
		// auto policy. This keeps the CLI's one-shot dialog command usable even
		// when navigation already installed the persistent auto handler.
		if raw, ok := dialogAutoWaiters.Load(page); ok {
			waiter := raw.(*dialogWaiter)
			if dialogAutoWaiters.CompareAndDelete(page, waiter) {
				accept, prompt := waiter.accept, waiter.prompt
				err := proto.PageHandleJavaScriptDialog{
					Accept:     accept,
					PromptText: prompt,
				}.Call(page)
				action := dialogAction(accept)
				result := &DialogResult{
					Handled:       err == nil,
					Action:        action,
					Type:          string(event.Type),
					Message:       event.Message,
					URL:           event.URL,
					DefaultPrompt: event.DefaultPrompt,
				}
				if err != nil {
					waiter.done <- dialogOutcome{err: err}
				} else {
					waiter.done <- dialogOutcome{result: result}
				}
				return
			}
		}
		currentPolicy := policy
		if current, ok := dialogAutoPolicies.Load(page); ok {
			currentPolicy = current.(*DialogAutoPolicy)
		}
		accept, prompt := currentPolicy.Snapshot()
		_ = proto.PageHandleJavaScriptDialog{
			Accept:     accept,
			PromptText: prompt,
		}.Call(page)
	})()
}

// EnableDialogAutoAccept ensures the page has a dialog listener even when the
// caller has not configured an explicit policy. Existing explicit policies are
// preserved across navigation.
func EnableDialogAutoAccept(page *rod.Page) {
	if page == nil {
		return
	}
	policy := &DialogAutoPolicy{Accept: true}
	if existing, ok := dialogAutoPolicies.Load(page); ok {
		policy = existing.(*DialogAutoPolicy)
	}
	StartDialogAutoHandler(page, policy)
}

// fileChooserOnce is keyed by the underlying CDP session, not *rod.Page:
// Rod returns a fresh Page clone for every iframe.Frame() call. Pointer keys
// would therefore register another event listener on every extraction.
type pageSessionKey struct {
	browser *rod.Browser
	session proto.TargetSessionID
}

var fileChooserOnce sync.Map // pageSessionKey -> struct{}

// forgetPageEventState releases registry entries after a target is closed.
// The event goroutines are tied to Rod's page context and terminate with the
// target; deleting the keys prevents long-lived sessions that rotate tabs
// from retaining closed page/session identities.
func forgetPageEventState(page *rod.Page) {
	if page == nil {
		return
	}
	fileChooserOnce.Delete(pageSessionKey{browser: page.Browser(), session: page.SessionID})
	installedPageScripts.Delete(pageSessionKey{browser: page.Browser(), session: page.SessionID})
	dialogAutoOnce.Delete(page)
	dialogAutoPolicies.Delete(page)
	dialogAutoWaiters.Delete(page)
}

func forgetBrowserEventState(browser *rod.Browser) {
	if browser == nil {
		return
	}
	installedPageScripts.Range(func(key, _ any) bool {
		if key.(pageSessionKey).browser == browser {
			installedPageScripts.Delete(key)
		}
		return true
	})
	pages, err := browser.Pages()
	if err != nil {
		return
	}
	for _, page := range pages {
		forgetPageEventState(page)
	}
}

// StartFileChooserIntercept prevents native OS file dialogs from blocking
// clicks on <input type=file>. Upload still uses SetFiles.
func StartFileChooserIntercept(page *rod.Page) {
	if page == nil {
		return
	}
	_ = proto.PageSetInterceptFileChooserDialog{Enabled: true}.Call(page)
	key := pageSessionKey{browser: page.Browser(), session: page.SessionID}
	if _, loaded := fileChooserOnce.LoadOrStore(key, struct{}{}); !loaded {
		go page.EachEvent(func(e *proto.PageFileChooserOpened) {
			// Swallow the chooser. Native dialogs hang headless Chrome.
		})()
	}
}

// DialogResult describes how a JS dialog handler completed.
type DialogResult struct {
	Handled       bool   `json:"handled"`
	Action        string `json:"action"`
	Type          string `json:"type,omitempty"`
	Message       string `json:"message,omitempty"`
	URL           string `json:"url,omitempty"`
	DefaultPrompt string `json:"default_prompt,omitempty"`
	TimedOut      bool   `json:"timed_out,omitempty"`
}

// HandleNextDialog waits for the next JavaScript dialog and handles it.
// The timeout is propagated via context so wait() unblocks cleanly on timeout
// and no goroutine is leaked.
func HandleNextDialog(page *rod.Page, accept bool, promptText string, timeout time.Duration) (*DialogResult, error) {
	if page == nil {
		return nil, fmt.Errorf("dialog: page is nil")
	}
	ctx, cancel := context.WithTimeout(page.GetContext(), timeout)
	defer cancel()

	// Navigation installs the persistent auto handler. Route a one-shot
	// explicit request through that listener so it cannot lose a race to the
	// default auto-accept callback.
	if _, auto := dialogAutoOnce.Load(page); auto {
		waiter := &dialogWaiter{
			accept: accept,
			prompt: promptText,
			done:   make(chan dialogOutcome, 1),
		}
		if _, loaded := dialogAutoWaiters.LoadOrStore(page, waiter); loaded {
			return nil, fmt.Errorf("dialog: another handler is already waiting")
		}
		defer dialogAutoWaiters.CompareAndDelete(page, waiter)
		select {
		case outcome := <-waiter.done:
			if outcome.err != nil {
				return nil, outcome.err
			}
			return outcome.result, nil
		case <-ctx.Done():
			return &DialogResult{Action: dialogAction(accept), TimedOut: true}, nil
		}
	}

	scoped := page.Context(ctx)

	wait, handle := scoped.HandleDialog()

	type outcome struct {
		result *DialogResult
		err    error
	}
	done := make(chan outcome, 1)

	go func() {
		defer func() {
			// wait() may panic if the context is cancelled mid-call
			if r := recover(); r != nil {
				select {
				case done <- outcome{err: fmt.Errorf("dialog wait cancelled: %v", r)}:
				default:
				}
			}
		}()
		event := wait()
		if event == nil {
			return
		}
		if err := handle(&proto.PageHandleJavaScriptDialog{
			Accept:     accept,
			PromptText: promptText,
		}); err != nil {
			done <- outcome{err: err}
			return
		}
		action := "accept"
		if !accept {
			action = "dismiss"
		}
		done <- outcome{result: &DialogResult{
			Handled:       true,
			Action:        action,
			Type:          string(event.Type),
			Message:       event.Message,
			URL:           event.URL,
			DefaultPrompt: event.DefaultPrompt,
		}}
	}()

	select {
	case o := <-done:
		return o.result, o.err
	case <-ctx.Done():
		action := "accept"
		if !accept {
			action = "dismiss"
		}
		return &DialogResult{
			Action:   action,
			TimedOut: true,
		}, nil
	}
}

func dialogAction(accept bool) string {
	if accept {
		return "accept"
	}
	return "dismiss"
}

func actionBudget(page *rod.Page) time.Duration {
	if page == nil {
		return DefaultActionTimeout
	}
	if dl, ok := page.GetContext().Deadline(); ok {
		remain := time.Until(dl)
		if remain > 0 && remain < DefaultActionTimeout {
			return remain
		}
	}
	return DefaultActionTimeout
}

func settleAfterAction(page *rod.Page, d time.Duration) {
	if page == nil || d <= 0 {
		return
	}
	_ = page.WaitStable(d)
}
