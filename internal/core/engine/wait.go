package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// WaitForPage applies a supported page wait strategy.
//
// Strategies (ordered fastest → slowest typically):
//   - "domcontentloaded": fires when HTML parsed, before sub-resources. ~200-500ms
//     faster than "load" on real-world pages with images/iframes/3P scripts.
//   - "load":             "load" event — DOM + sub-resources fetched.
//   - "stable":           DOM mutations have settled for 500ms.
//   - "idle":             network has been idle for 500ms.
//   - "none":             return immediately.
//
// Defaults to "domcontentloaded" for empty input — that's the right trade-off
// for an LLM agent: parsing is done, refs are extractable, but we don't pay
// for late-loading ads/analytics.
func WaitForPage(page *rod.Page, waitStrategy string) error {
	switch waitStrategy {
	case "stable":
		return page.WaitStable(500 * time.Millisecond)
	case "idle", "networkidle":
		// Bound the idle wait: WaitRequestIdle's duration is *quiet time*, not a
		// deadline. A never-finishing XHR would otherwise stall the agent loop.
		target := page
		if timeout := navTimeout(page); timeout > 0 {
			target = page.Timeout(timeout)
		}
		target.WaitRequestIdle(500*time.Millisecond, nil, nil, nil)()
		return nil
	case "none":
		return nil
	case "load":
		return page.WaitLoad()
	case "", "domcontentloaded", "dcl":
		return waitDOMContentLoaded(page)
	default:
		return fmt.Errorf("invalid wait strategy %q: use domcontentloaded, load, stable, idle, or none", waitStrategy)
	}
}

func waitDOMContentLoaded(page *rod.Page) error {
	if page == nil {
		return fmt.Errorf("page is nil")
	}
	ready, err := page.Eval(`() => document.readyState`)
	if err == nil && ready != nil {
		switch strings.ToLower(fmt.Sprint(ready.Value.Val())) {
		case "interactive", "complete":
			return nil
		}
	}
	_ = proto.PageSetLifecycleEventsEnabled{Enabled: true}.Call(page)
	wait := page.EachEvent(func(e *proto.PageLifecycleEvent) bool {
		return e != nil && e.Name == proto.PageLifecycleEventNameDOMContentLoaded && (page.FrameID == "" || e.FrameID == page.FrameID)
	})
	wait()
	return nil
}

// WaitForText polls until visible body text contains needle.
func WaitForText(page *rod.Page, needle string, timeout time.Duration) error {
	if page == nil {
		return fmt.Errorf("page is nil")
	}
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return fmt.Errorf("text is required")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	interval := 100 * time.Millisecond
	for {
		body, err := page.Element("body")
		if err == nil && body != nil {
			if pageText, err := body.Text(); err == nil && strings.Contains(pageText, needle) {
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("wait for text %q timed out after %s", needle, timeout)
		}
		time.Sleep(interval)
	}
}

// WaitForURL polls until the current URL contains needle (substring or glob-lite **).
func WaitForURL(page *rod.Page, needle string, timeout time.Duration) error {
	if page == nil {
		return fmt.Errorf("page is nil")
	}
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return fmt.Errorf("url is required")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	pattern := strings.ReplaceAll(needle, "**", "")
	for {
		info, err := page.Info()
		if err == nil && info != nil && info.URL != "" {
			if strings.Contains(info.URL, pattern) || strings.Contains(info.URL, needle) {
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("wait for url %q timed out after %s", needle, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// WaitSpec is the JSONL/MCP wait contract. Conditions run in this order:
// load/url/text/target, then a fixed delay. Empty spec is a no-op.
type WaitSpec struct {
	Selector string
	Ref      string
	Text     string
	URL      string
	Load     string
	State    string
	MS       int
	Timeout  time.Duration
}

// WaitForAgent waits for page/element/text/url conditions used by JSONL and MCP.
func WaitForAgent(page *rod.Page, b *Browser, snapshot *PageSnapshot, spec WaitSpec, timeout time.Duration) (interface{}, error) {
	if page == nil {
		return nil, fmt.Errorf("page is nil")
	}
	if spec.Timeout > 0 {
		timeout = spec.Timeout
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if spec.Load != "" {
		if err := WaitForPage(page, spec.Load); err != nil {
			return nil, fmt.Errorf("wait load %q: %w", spec.Load, err)
		}
	}
	if spec.URL != "" {
		if err := WaitForURL(page, spec.URL, timeout); err != nil {
			return nil, err
		}
	}
	if spec.Text != "" {
		if err := WaitForText(page, spec.Text, timeout); err != nil {
			return nil, err
		}
	}
	target := strings.TrimSpace(spec.Ref)
	if target == "" {
		target = strings.TrimSpace(spec.Selector)
	}
	if target != "" {
		state := StateVisible
		if spec.State != "" {
			parsed, err := ParseElementState(spec.State)
			if err != nil {
				return nil, err
			}
			state = parsed
		}
		snap := snapshot
		if snap == nil && b != nil {
			snap = b.Snapshot(page)
		}
		if _, err := WaitForTarget(page, target, snap, state, timeout); err != nil {
			return nil, fmt.Errorf("wait %q: %w", target, err)
		}
	}
	if spec.MS > 0 {
		time.Sleep(time.Duration(spec.MS) * time.Millisecond)
	}
	return nil, nil
}
