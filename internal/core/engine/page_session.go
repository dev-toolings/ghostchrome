package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ErrAmbiguousRef means a stale backend node matched more than one live
// role+name candidate. The agent must re-extract instead of guessing.
var ErrAmbiguousRef = errors.New("ambiguous ref: multiple elements match role+name")

// PageSession is the per-target execution handle: budgets, refs, and
// actionability live here so CLI/JSONL/MCP share one interaction path.
type PageSession struct {
	rt      *Runtime
	page    *rod.Page
	timeout time.Duration
}

func (s *PageSession) Page() *rod.Page {
	if s == nil {
		return nil
	}
	return s.page
}

func (s *PageSession) Timeout() time.Duration {
	if s == nil || s.timeout <= 0 {
		return DefaultActionTimeout
	}
	return s.timeout
}

// ResolveRefSemantic resolves @N by backend node id, then by role+name+nth
// when the node was replaced (typical SPA rerender without navigation).
func ResolveRefSemantic(page *rod.Page, ref string, snapshot *PageSnapshot) (*rod.Element, error) {
	parsed, err := parseRef(ref)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, fmt.Errorf("%w: run preview, extract, or navigate --extract first", ErrStaleRef)
	}
	info, ok := snapshot.Refs[parsed]
	if !ok || (info.BackendNodeID == 0 && info.Role == "") {
		return nil, fmt.Errorf("%w: ref %s not found in last snapshot", ErrStaleRef, parsed)
	}
	if info.BackendNodeID != 0 {
		src := pageForFrame(page, info.FrameID)
		if el, err := liveElementFromBackend(src, info.BackendNodeID); err == nil {
			return el, nil
		}
	}
	if strings.TrimSpace(info.Role) == "" {
		return nil, fmt.Errorf("%w: ref %s is no longer attached", ErrStaleRef, parsed)
	}
	el, err := resolveByRoleNameNth(pageForFrame(page, info.FrameID), info.Role, info.Name, info.Nth)
	if err != nil {
		return nil, fmt.Errorf("%w: ref %s", err, parsed)
	}
	return el, nil
}

func pageForFrame(page *rod.Page, frame proto.PageFrameID) *rod.Page {
	if page == nil || frame == "" || page.FrameID == frame {
		return page
	}
	return pageForFrameAt(page, frame, 0)
}

func pageForFrameAt(page *rod.Page, frame proto.PageFrameID, depth int) *rod.Page {
	if page == nil || frame == "" || page.FrameID == frame || depth >= maxIframeDepth {
		return page
	}
	iframes, err := page.Elements("iframe")
	if err != nil {
		return page
	}
	for _, iframe := range iframes {
		child, err := iframe.Frame()
		if err != nil || child == nil {
			continue
		}
		StartFileChooserIntercept(child)
		if child.FrameID == frame {
			return child
		}
		if nested := pageForFrameAt(child, frame, depth+1); nested != nil && nested.FrameID == frame {
			return nested
		}
	}
	return page
}

func liveElementFromBackend(page *rod.Page, nodeID proto.DOMBackendNodeID) (*rod.Element, error) {
	// Bound so a dead backend id cannot inherit the 8s action timeout and
	// block semantic fallback. Reattach the element to the parent page so
	// the 250ms probe does not leak onto the subsequent click/type.
	src := page
	if page != nil {
		src = page.Timeout(250 * time.Millisecond)
	}
	el, err := src.ElementFromNode(&proto.DOMNode{BackendNodeID: nodeID})
	if err != nil {
		return nil, err
	}
	// ElementFromNode can return a wrapper for a backend node that was
	// detached between the snapshot and this lookup. Verify attachment before
	// treating it as a successful semantic resolution; otherwise ResolveRef
	// may silently hand callers a dead element and skip the role/name fallback.
	connected, err := el.Eval(`() => this.isConnected`)
	if err != nil {
		return nil, err
	}
	if connected == nil || connected.Value.Val() != true {
		return nil, fmt.Errorf("element is detached")
	}
	if page != nil {
		el = el.Context(page.GetContext())
	}
	return el, nil
}

func resolveByRoleNameNth(page *rod.Page, role, name string, nth int) (*rod.Element, error) {
	matches, err := queryAXByRoleName(page, role, name)
	if err != nil || len(matches) == 0 {
		full, fullErr := fullAXByRoleName(page, role, name)
		if fullErr != nil {
			if err != nil {
				return nil, err
			}
			return nil, fullErr
		}
		matches = full
	}
	idx, err := chooseSemanticMatch(len(matches), nth)
	if err != nil {
		return nil, err
	}
	return matches[idx], nil
}

func collectAXMatches(page *rod.Page, nodes []*proto.AccessibilityAXNode, role, name string) []*rod.Element {
	wantRole := strings.ToLower(strings.TrimSpace(role))
	wantName := strings.ToLower(strings.TrimSpace(name))
	matches := make([]*rod.Element, 0, 4)
	seen := map[proto.DOMBackendNodeID]struct{}{}
	for _, n := range nodes {
		if n == nil || n.BackendDOMNodeID == 0 {
			continue
		}
		if strings.ToLower(axValueStr(n.Role)) != wantRole {
			continue
		}
		if strings.ToLower(strings.TrimSpace(axValueStr(n.Name))) != wantName {
			continue
		}
		if _, ok := seen[n.BackendDOMNodeID]; ok {
			continue
		}
		el, err := liveElementFromBackend(page, n.BackendDOMNodeID)
		if err != nil {
			continue
		}
		seen[n.BackendDOMNodeID] = struct{}{}
		matches = append(matches, el)
	}
	return matches
}

func queryAXByRoleName(page *rod.Page, role, name string) ([]*rod.Element, error) {
	doc, err := proto.DOMGetDocument{}.Call(page)
	if err != nil || doc == nil || doc.Root == nil || doc.Root.BackendNodeID == 0 {
		if err == nil {
			err = fmt.Errorf("a11y query: missing document root")
		}
		return nil, err
	}
	q := proto.AccessibilityQueryAXTree{
		BackendNodeID: doc.Root.BackendNodeID,
		Role:          strings.TrimSpace(role),
	}
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		q.AccessibleName = trimmed
	}
	result, err := q.Call(page)
	if err != nil {
		return nil, fmt.Errorf("a11y query: %w", err)
	}
	return collectAXMatches(page, result.Nodes, role, name), nil
}

func fullAXByRoleName(page *rod.Page, role, name string) ([]*rod.Element, error) {
	result, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("a11y tree: %w", err)
	}
	return collectAXMatches(page, result.Nodes, role, name), nil
}

func chooseSemanticMatch(count, nth int) (int, error) {
	if count == 0 {
		return 0, ErrStaleRef
	}
	if nth < 0 {
		nth = 0
	}
	if count == 1 {
		return 0, nil
	}
	if nth >= count {
		return 0, ErrAmbiguousRef
	}
	return nth, nil
}

// EnsureActionable waits until el is visible, enabled, and geometrically stable.
// A successful probe returns "" or "popup" (target=_blank / window.open) so a
// later click can wait for Target.targetCreated without scanning Pages().
const actionableJS = `() => {
	const el = this;
	const popup = (() => {
		const a = el.closest ? el.closest('a') : (el.tagName === 'A' ? el : null);
		const node = a || el;
		const href = String((node && node.href) || (node.getAttribute && node.getAttribute('href')) || '').toLowerCase();
		const target = String((node && node.target) || (node.getAttribute && node.getAttribute('target')) || '').toLowerCase();
		if (target === '_blank' || href.indexOf('javascript:window.open') !== -1 || href.indexOf('window.open(') !== -1) return 'popup';
		const tag = String((node && node.tagName) || '').toLowerCase();
		const type = String((node && node.type) || (node.getAttribute && node.getAttribute('type')) || '').toLowerCase();
		const form = node && node.form;
		const navHref = href && href !== '#' && href.indexOf('javascript:') !== 0;
		const download = String((node && node.download) || (node.getAttribute && node.getAttribute('download')) || '').toLowerCase();
		const looksDownload = download !== '' || /\.(pdf|zip|csv|xlsx?|docx?|png|jpe?g|gz|tgz|tar)(\?|$)/.test(href) || href.indexOf('content-disposition=attachment') !== -1;
		if (tag === 'a' && navHref && looksDownload) return 'download';
		if (tag === 'a' && navHref && target !== '_self') return 'nav';
		if (tag === 'a' && navHref && (target === '' || target === '_self')) return 'nav';
		// HTML default button type is submit; only treat it as navigation inside a form.
		if (form && tag === 'button' && type !== 'button' && type !== 'reset') return 'nav';
		if (form && tag === 'input' && (type === 'submit' || type === 'image')) return 'nav';
		return '';
	})();
	if (!el.isConnected) return 'detached';
	const style = window.getComputedStyle(el);
	if (style.visibility === 'hidden' || style.display === 'none' || Number(style.opacity) === 0) return 'not visible';
	if (el.disabled) return 'disabled';
	const rect = el.getBoundingClientRect();
	if (rect.width <= 0 || rect.height <= 0) return 'empty box';
	const x = rect.x + rect.width / 2;
	const y = rect.y + rect.height / 2;
	if (x < 0 || y < 0 || x > window.innerWidth || y > window.innerHeight) {
		el.scrollIntoView({block:'center', inline:'nearest'});
	}
	const r2 = el.getBoundingClientRect();
	const px = r2.x + r2.width / 2;
	const py = r2.y + r2.height / 2;
	const hit = (function pierce(root, hx, hy) {
		const node = (root.elementFromPoint ? root.elementFromPoint(hx, hy) : null);
		if (!node) return null;
		if (node.shadowRoot) {
			const inner = pierce(node.shadowRoot, hx, hy);
			if (inner) return inner;
		}
		return node;
	})(document, px, py);
	if (!hit || el === hit || el.contains(hit) || hit.contains(el)) return popup;
	if (hit.shadowRoot && (hit.shadowRoot.contains(el) || el.getRootNode() === hit.shadowRoot)) return popup;
	const hitLabel = hit.closest && hit.closest('label');
	if (hitLabel && (hitLabel.control === el || hitLabel.contains(el))) return popup;
	let desc = (hit.tagName || 'unknown').toLowerCase();
	if (hit.id) desc += '#' + hit.id;
	return 'covered by <' + desc + '>';
}`

func checkActionable(el *rod.Element) error {
	if el == nil {
		return errors.New("element is nil")
	}
	res, err := el.Eval(actionableJS)
	if err != nil {
		return err
	}
	if res == nil {
		return nil
	}
	reason := strings.TrimSpace(fmt.Sprint(res.Value.Val()))
	if reason == "" || reason == "<nil>" || reason == "popup" || reason == "nav" || reason == "download" {
		setClickPopupHint(el.Page(), reason == "popup")
		setClickNavHint(el.Page(), reason == "nav")
		setClickDownloadHint(el.Page(), reason == "download")
		return nil
	}
	if strings.HasPrefix(reason, "covered by") {
		return fmt.Errorf("element is %s at its click point", reason)
	}
	return fmt.Errorf("element is %s", reason)
}

func EnsureActionable(el *rod.Element, timeout time.Duration) error {
	if el == nil {
		return errors.New("element is nil")
	}
	check := func() error {
		return checkActionable(el)
	}
	if timeout <= 0 {
		return check()
	}
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		last = check()
		if last == nil {
			return nil
		}
		if isPermanentActionError(last) {
			return fmt.Errorf("actionability: %w", last)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("element not actionable")
	}
	return fmt.Errorf("actionability: %w", last)
}

func isPermanentActionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrStaleRef) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "detached") || strings.Contains(msg, "element is nil")
}

func withSemanticRetry(page *rod.Page, ref string, snapshot *PageSnapshot, fn func(*rod.Element) error) error {
	el, err := ResolveRefSemantic(page, ref, snapshot)
	if err != nil {
		return err
	}
	err = fn(el)
	if err == nil || !isDetachedActionError(err) {
		return err
	}
	parsed, perr := parseRef(ref)
	if perr != nil || snapshot == nil {
		return err
	}
	info, ok := snapshot.Refs[parsed]
	if !ok || strings.TrimSpace(info.Role) == "" {
		return err
	}
	replaced, rerr := resolveByRoleNameNth(page, info.Role, info.Name, info.Nth)
	if rerr != nil {
		return fmt.Errorf("%w: ref %s", rerr, parsed)
	}
	return fn(replaced)
}

func isDetachedActionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrStaleRef) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "detached") || strings.Contains(msg, "not attached") || strings.Contains(msg, "cannot find context")
}

func (s *PageSession) Click(ref string, snapshot *PageSnapshot, button proto.InputMouseButton) error {
	if s == nil || s.page == nil {
		return errors.New("page session is closed")
	}
	return withSemanticRetry(s.page, ref, snapshot, func(el *rod.Element) error {
		return ClickElementWithButton(s.page, el, button)
	})
}

func (s *PageSession) ClickRef(ref string, snapshot *PageSnapshot) error {
	return s.Click(ref, snapshot, proto.InputMouseButtonLeft)
}

func (s *PageSession) DblClick(ref string, snapshot *PageSnapshot, button proto.InputMouseButton) error {
	if s == nil || s.page == nil {
		return errors.New("page session is closed")
	}
	return withSemanticRetry(s.page, ref, snapshot, func(el *rod.Element) error {
		return dblClickElementWithButton(s.page, el, button)
	})
}

func (s *PageSession) Type(ref, text string, snapshot *PageSnapshot) error {
	if s == nil || s.page == nil {
		return errors.New("page session is closed")
	}
	return withSemanticRetry(s.page, ref, snapshot, func(el *rod.Element) error {
		return TypeElement(s.page, el, text)
	})
}

func (s *PageSession) Hover(ref string, snapshot *PageSnapshot) error {
	if s == nil || s.page == nil {
		return errors.New("page session is closed")
	}
	return withSemanticRetry(s.page, ref, snapshot, func(el *rod.Element) error {
		return HoverElement(s.page, el)
	})
}

func (s *PageSession) Select(ref string, values []string, snapshot *PageSnapshot) error {
	if s == nil || s.page == nil {
		return errors.New("page session is closed")
	}
	return SelectOption(s.page, ref, values, snapshot)
}

func (s *PageSession) Check(ref string, checked bool, snapshot *PageSnapshot) error {
	if s == nil || s.page == nil {
		return errors.New("page session is closed")
	}
	return SetCheckedRef(s.page, ref, checked, snapshot)
}

func (s *PageSession) CaptureMutation(prev *PageSnapshot) SnapshotDiff {
	if s == nil || s.page == nil {
		return SnapshotDiff{Unchanged: true}
	}
	var b *Browser
	if s.rt != nil {
		b = s.rt.Browser
	}
	diff, _, err := CaptureMutation(b, s.page, prev)
	if err != nil {
		return SnapshotDiff{Unchanged: true}
	}
	return diff
}

const hitTargetJS = `() => {
	const el = this;
	const rect = el.getBoundingClientRect();
	if (rect.width <= 0 || rect.height <= 0) return 'empty box';
	const x = rect.x + rect.width / 2;
	const y = rect.y + rect.height / 2;
	const hit = (function pierce(root, px, py) {
		const node = (root.elementFromPoint ? root.elementFromPoint(px, py) : null);
		if (!node) return null;
		if (node.shadowRoot) {
			const inner = pierce(node.shadowRoot, px, py);
			if (inner) return inner;
		}
		return node;
	})(document, x, y);
	if (!hit || el === hit || el.contains(hit) || hit.contains(el)) return '';
	if (hit.shadowRoot && (hit.shadowRoot.contains(el) || el.getRootNode() === hit.shadowRoot)) return '';
	const hitLabel = hit.closest && hit.closest('label');
	if (hitLabel && (hitLabel.control === el || hitLabel.contains(el))) return '';
	let desc = (hit.tagName || 'unknown').toLowerCase();
	if (hit.id) desc += '#' + hit.id;
	return desc;
}`

// CheckHitTarget fails when another element would receive the click at the
// target's center. Label/control pairing is allowed.
func CheckHitTarget(el *rod.Element) error {
	if el == nil {
		return errors.New("element is nil")
	}
	res, err := el.Eval(hitTargetJS)
	if err != nil || res == nil {
		return nil
	}
	reason := strings.TrimSpace(fmt.Sprint(res.Value.Val()))
	if reason == "" || reason == "<nil>" {
		return nil
	}
	return fmt.Errorf("element is covered by <%s> at its click point", reason)
}
