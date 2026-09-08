// Package mcp tool registrations.
//
// MCP v2.0 surface: 19 tools for an LLM-agent browser loop (snapshot, navigate,
// click, type, select, press, wait_for, eval, screenshot, hover, drag, swipe,
// emulate, fill_form, upload, tabs, dialog, back, forward). Everything that
// isn't on the hot path (sniff/trace/cookies/storage/blocker_stats) lives in
// the CLI only — adding tools here has a real token cost in every `tools/list`
// the model receives, so we stay deliberately small.
package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/antibot"
	"github.com/dev-toolings/ghostchrome/internal/core/interact"
	"github.com/dev-toolings/ghostchrome/internal/core/overlay"
	"github.com/go-rod/rod"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
)

// registerTools wires every MCP tool. The set is intentionally small —
// snapshot covers preview/extract/errors, type covers fill_form, eval is
// the escape hatch for everything else.
func registerTools(srv *mcpsrv.MCPServer, s *Server) {
	srv.AddTool(mcpgo.NewTool("snapshot",
		mcpgo.WithDescription("All-in-one page report: status, console+network errors, compact DOM with refs (@1, @2, ...). If `url` is given, navigate first; otherwise snapshot the current page. This is the canonical first call when an agent visits or revisits a page. Refs from this snapshot stay valid until the next snapshot or navigate."),
		mcpgo.WithString("url", mcpgo.Description("Absolute URL to navigate to before snapshotting (optional — omit to snapshot the current page)")),
		mcpgo.WithString("wait", mcpgo.Description("Wait strategy when navigating: domcontentloaded (default), load, stable, idle, none"), mcpgo.Enum("domcontentloaded", "load", "stable", "idle", "none"), mcpgo.DefaultString("domcontentloaded")),
		mcpgo.WithString("level", mcpgo.Description("DOM extraction depth: skeleton (interactive only, smallest), content (default — adds text), full (everything named)"), mcpgo.Enum("skeleton", "content", "full"), mcpgo.DefaultString("content")),
		mcpgo.WithString("selector", mcpgo.Description("Optional CSS selector to scope the DOM extraction to a subtree")),
	), s.handleSnapshot)

	srv.AddTool(mcpgo.NewTool("navigate",
		mcpgo.WithDescription("Navigate to a URL without a full snapshot. Returns only status + title + load time. Use when you don't need the DOM yet (e.g. chaining click-through pages)."),
		mcpgo.WithString("url", mcpgo.Required(), mcpgo.Description("Absolute URL to navigate to (https://...)")),
		mcpgo.WithString("wait", mcpgo.Description("domcontentloaded (default), load, stable, idle, none"), mcpgo.Enum("domcontentloaded", "load", "stable", "idle", "none"), mcpgo.DefaultString("domcontentloaded")),
	), s.handleNavigate)

	srv.AddTool(mcpgo.NewTool("click",
		mcpgo.WithDescription("Click an element by its ref (@1, @2, ...). Ref comes from the last snapshot. Auto-waits for attached + visible + enabled + hit-target, then returns a compact a11y-ref diff (snapshot=diff, default), a skeleton extract (full), or nothing (none)."),
		mcpgo.WithString("ref", mcpgo.Required(), mcpgo.Description("Element ref from the last snapshot (e.g. @3 or 3)")),
		mcpgo.WithString("button", mcpgo.Description("Mouse button: left, right, or middle (default: left)"), mcpgo.Enum("left", "right", "middle"), mcpgo.DefaultString("left")),
		mcpgo.WithString("snapshot", mcpgo.Description("none | diff | full (default: diff)"), mcpgo.Enum("none", "diff", "full"), mcpgo.DefaultString("diff")),
	), s.handleClick)

	srv.AddTool(mcpgo.NewTool("type",
		mcpgo.WithDescription("Type text into an input/textarea by ref. Set submit=true to press Enter after typing. Returns a compact a11y-ref diff (snapshot=diff, default), a skeleton extract (full), or nothing (none)."),
		mcpgo.WithString("ref", mcpgo.Required(), mcpgo.Description("Element ref from the last snapshot")),
		mcpgo.WithString("text", mcpgo.Required(), mcpgo.Description("Text to type (the field is cleared first)")),
		mcpgo.WithBoolean("submit", mcpgo.Description("If true, press Enter after typing"), mcpgo.DefaultBool(false)),
		mcpgo.WithString("snapshot", mcpgo.Description("none | diff | full (default: diff)"), mcpgo.Enum("none", "diff", "full"), mcpgo.DefaultString("diff")),
	), s.handleType)

	srv.AddTool(mcpgo.NewTool("select",
		mcpgo.WithDescription("Select one or more options in a <select> element by ref. Returns a compact a11y-ref diff (snapshot=diff, default), a skeleton extract (full), or nothing (none)."),
		mcpgo.WithString("ref", mcpgo.Required(), mcpgo.Description("Element ref of the <select>")),
		mcpgo.WithString("value", mcpgo.Required(), mcpgo.Description("Option value or visible label to select. For multi-select, pass JSON array as string (e.g. \"[\\\"a\\\",\\\"b\\\"]\").")),
		mcpgo.WithString("snapshot", mcpgo.Description("none | diff | full (default: diff)"), mcpgo.Enum("none", "diff", "full"), mcpgo.DefaultString("diff")),
	), s.handleSelect)

	srv.AddTool(mcpgo.NewTool("press",
		mcpgo.WithDescription("Press a keyboard key. If `ref` is provided, focus that element first; otherwise the key fires on the document."),
		mcpgo.WithString("key", mcpgo.Required(), mcpgo.Description("Key name: Enter, Tab, Escape, ArrowDown, ArrowUp, PageDown, Backspace, ...")),
		mcpgo.WithString("ref", mcpgo.Description("Optional element ref to focus before pressing")),
	), s.handlePress)

	srv.AddTool(mcpgo.NewTool("wait_for",
		mcpgo.WithDescription("Wait for a condition: @ref or CSS selector to become visible, text to appear, or just a timeout. At least one of ref/selector/text/timeout_ms must be given."),
		mcpgo.WithString("ref", mcpgo.Description("Element ref from the last snapshot (e.g. @3)")),
		mcpgo.WithString("selector", mcpgo.Description("CSS selector to wait for")),
		mcpgo.WithString("text", mcpgo.Description("Visible text substring to wait for")),
		mcpgo.WithString("url", mcpgo.Description("Wait until the current URL contains this substring")),
		mcpgo.WithString("load", mcpgo.Description("Page load state: load, domcontentloaded, idle, stable, none"), mcpgo.Enum("load", "domcontentloaded", "idle", "stable", "none")),
		mcpgo.WithString("state", mcpgo.Description("Element state when waiting on ref/selector: attached, visible, hidden, enabled, stable (default: visible)"), mcpgo.Enum("attached", "visible", "hidden", "enabled", "stable"), mcpgo.DefaultString("visible")),
		mcpgo.WithNumber("timeout_ms", mcpgo.Description("Maximum wait time in milliseconds (default 5000, max 30000)"), mcpgo.DefaultNumber(5000)),
	), s.handleWaitFor)

	srv.AddTool(mcpgo.NewTool("eval",
		mcpgo.WithDescription("Evaluate a JavaScript expression in the page and return its serialized value. Async expressions are awaited. Use this as an escape hatch for anything the other tools can't do (read localStorage, dispatch a custom event, scroll, etc.)."),
		mcpgo.WithString("expression", mcpgo.Required(), mcpgo.Description("JS expression. Top-level await OK.")),
		mcpgo.WithString("ref", mcpgo.Description("Optional @ref to scope `this` to an element")),
		mcpgo.WithNumber("timeout_ms", mcpgo.Description("Per-call deadline in milliseconds (default 8000)"), mcpgo.DefaultNumber(8000)),
	), s.handleEval)

	srv.AddTool(mcpgo.NewTool("screenshot",
		mcpgo.WithDescription("Capture the current page (or one element by ref) as an image embedded in the MCP result. Defaults to WebP quality 60 — typically 30-70% lighter than JPEG/PNG for UI captures. Use annotate=true to overlay numbered borders on interactive elements."),
		mcpgo.WithString("ref", mcpgo.Description("Capture only this element by @ref")),
		mcpgo.WithBoolean("full_page", mcpgo.Description("Capture the full scrollable page instead of the viewport"), mcpgo.DefaultBool(false)),
		mcpgo.WithString("format", mcpgo.Description("Image format: webp (default), jpeg, png"), mcpgo.Enum("webp", "jpeg", "png"), mcpgo.DefaultString("webp")),
		mcpgo.WithNumber("quality", mcpgo.Description("Quality 1-100 for webp/jpeg (default 60). Ignored for png."), mcpgo.DefaultNumber(60)),
		mcpgo.WithBoolean("annotate", mcpgo.Description("Overlay numbered borders on interactive elements (forces PNG output)"), mcpgo.DefaultBool(false)),
	), s.handleScreenshot)

	srv.AddTool(mcpgo.NewTool("hover",
		mcpgo.WithDescription("Hover over an element by ref. Returns a compact a11y-ref diff (snapshot=diff, default), a skeleton extract (full), or nothing (none)."),
		mcpgo.WithString("ref", mcpgo.Required(), mcpgo.Description("Element ref from the last snapshot")),
		mcpgo.WithString("snapshot", mcpgo.Description("none | diff | full (default: diff)"), mcpgo.Enum("none", "diff", "full"), mcpgo.DefaultString("diff")),
	), s.handleHover)

	srv.AddTool(mcpgo.NewTool("drag",
		mcpgo.WithDescription("Drag an element from one ref to another. Simulates a full mouse drag-and-drop sequence."),
		mcpgo.WithString("from", mcpgo.Required(), mcpgo.Description("Source element ref")),
		mcpgo.WithString("to", mcpgo.Required(), mcpgo.Description("Target element ref")),
		mcpgo.WithNumber("steps", mcpgo.Description("Intermediate mouse move steps (default 10)"), mcpgo.DefaultNumber(10)),
	), s.handleDrag)

	srv.AddTool(mcpgo.NewTool("swipe",
		mcpgo.WithDescription("Swipe with a real single-finger touch gesture (touchstart/touchmove/touchend) between two viewport coordinates in CSS pixels. Use this — not drag — to test a mobile drawer, carousel, or pull-to-refresh: drag emits mouse events, which a touch-only handler ignores. Enables touch emulation automatically if it is off."),
		mcpgo.WithNumber("from_x", mcpgo.Required(), mcpgo.Description("Start X in CSS pixels, relative to the viewport")),
		mcpgo.WithNumber("from_y", mcpgo.Required(), mcpgo.Description("Start Y in CSS pixels, relative to the viewport")),
		mcpgo.WithNumber("to_x", mcpgo.Required(), mcpgo.Description("End X in CSS pixels")),
		mcpgo.WithNumber("to_y", mcpgo.Required(), mcpgo.Description("End Y in CSS pixels")),
		mcpgo.WithNumber("duration_ms", mcpgo.Description("Gesture duration in milliseconds (default 300). Shorter = flick, longer = slow drag."), mcpgo.DefaultNumber(300)),
		mcpgo.WithNumber("steps", mcpgo.Description("Intermediate touchmove events (default 12)"), mcpgo.DefaultNumber(12)),
		mcpgo.WithString("snapshot", mcpgo.Description("none | diff | full (default: diff)"), mcpgo.Enum("none", "diff", "full"), mcpgo.DefaultString("diff")),
	), s.handleSwipe)

	srv.AddTool(mcpgo.NewTool("emulate",
		mcpgo.WithDescription("Emulate a device: viewport size, devicePixelRatio, mobile flag, touch input, user-agent, and prefers-color-scheme. Use it before testing a responsive or mobile-only UI — without it the page is a 1920x1080 desktop with pointer:fine, so a phone shell or a coarse-pointer media query never activates. Pass a preset via `device`, or explicit width/height. Pass reset=true to go back to a plain desktop tab. Invalidates refs: re-snapshot afterwards."),
		mcpgo.WithString("device", mcpgo.Description("Preset: iphone-se, iphone-14, iphone-14-pro, iphone-14-pro-max, pixel-7, pixel-8-pro, ipad, ipad-pro, desktop, desktop-2k")),
		mcpgo.WithNumber("width", mcpgo.Description("Viewport width in CSS pixels (overrides the preset)")),
		mcpgo.WithNumber("height", mcpgo.Description("Viewport height in CSS pixels (overrides the preset)")),
		mcpgo.WithNumber("device_scale_factor", mcpgo.Description("devicePixelRatio, e.g. 3 for a modern iPhone (default 1, or the preset's)")),
		mcpgo.WithBoolean("mobile", mcpgo.Description("Mobile viewport semantics: meta viewport, mobile scrollbars, screen size")),
		mcpgo.WithBoolean("touch", mcpgo.Description("Touch input emulation: pointer:coarse, navigator.maxTouchPoints > 0, touch events")),
		mcpgo.WithString("user_agent", mcpgo.Description("Override navigator.userAgent and the User-Agent header")),
		mcpgo.WithString("color_scheme", mcpgo.Description("Emulate prefers-color-scheme"), mcpgo.Enum("dark", "light", "no-preference")),
		mcpgo.WithBoolean("reset", mcpgo.Description("Drop every emulation override and restore the real desktop viewport and UA"), mcpgo.DefaultBool(false)),
	), s.handleEmulate)

	srv.AddTool(mcpgo.NewTool("fill_form",
		mcpgo.WithDescription("Fill multiple form fields in one call. Pass a JSON object mapping refs to values: {\"@1\": \"John\", \"@2\": \"john@example.com\"}"),
		mcpgo.WithString("fields", mcpgo.Required(), mcpgo.Description("JSON object mapping @ref to text value")),
	), s.handleFillForm)

	srv.AddTool(mcpgo.NewTool("upload",
		mcpgo.WithDescription("Upload file(s) to a file input element by ref."),
		mcpgo.WithString("ref", mcpgo.Required(), mcpgo.Description("File input element ref")),
		mcpgo.WithString("paths", mcpgo.Required(), mcpgo.Description("File path or JSON array of file paths")),
	), s.handleUpload)

	srv.AddTool(mcpgo.NewTool("tabs",
		mcpgo.WithDescription("List open browser tabs with their URLs, titles, and indices. Use the index with 'switch' action to change the active tab."),
		mcpgo.WithString("action", mcpgo.Description("list (default), switch, close, new"), mcpgo.Enum("list", "switch", "close", "new"), mcpgo.DefaultString("list")),
		mcpgo.WithNumber("index", mcpgo.Description("Tab index for switch/close actions")),
		mcpgo.WithString("url", mcpgo.Description("URL for action=new (blank tab when omitted)")),
	), s.handleTabs)

	srv.AddTool(mcpgo.NewTool("dialog",
		mcpgo.WithDescription("Set how JavaScript dialogs (alert/confirm/prompt) are handled. Default is accept. Applies to subsequent dialogs."),
		mcpgo.WithString("action", mcpgo.Description("accept (default) or dismiss"), mcpgo.Enum("accept", "dismiss"), mcpgo.DefaultString("accept")),
		mcpgo.WithString("text", mcpgo.Description("Prompt response text when action=accept")),
	), s.handleDialog)

	srv.AddTool(mcpgo.NewTool("back",
		mcpgo.WithDescription("Navigate back in browser history. Returns the new URL."),
	), s.handleBack)

	srv.AddTool(mcpgo.NewTool("forward",
		mcpgo.WithDescription("Navigate forward in browser history. Returns the new URL."),
	), s.handleForward)
}

// ============================================================================
// handlers
// ============================================================================

func (s *Server) handleSnapshot(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	url := mcpgo.ParseString(req, "url", "")
	wait := mcpgo.ParseString(req, "wait", "domcontentloaded")
	levelStr := mcpgo.ParseString(req, "level", "content")
	selector := mcpgo.ParseString(req, "selector", "")

	level := engine.ExtractLevel(levelStr)
	if err := engine.ValidateExtractLevel(level); err != nil {
		return errResult(err)
	}

	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		// With URL: full Preview (navigate + errors + network + extract).
		if url != "" {
			pv, err := engine.Preview(page, url, wait, level, func(p *rod.Page) error {
				if s.opts.DismissCookies {
					if antibot.DismissCookieBanner(p) {
						_ = engine.WaitForPage(p, "stable")
					}
				}
				return nil
			}, s.opts.Stealth)
			if err != nil {
				return errResult(fmt.Errorf("snapshot: %w", err))
			}
			if pv.DOM != nil {
				s.rememberSnapshot(page, pv.DOM)
			}
			return previewResult(pv)
		}

		// Without URL: snapshot the current page. Errors/network come from
		// the long-lived observer (started in ensurePageLocked).
		info, err := page.Info()
		if err != nil {
			return errResult(fmt.Errorf("page info: %w", err))
		}
		extracted, err := engine.Extract(page, level, selector, false)
		if err != nil {
			return errResult(fmt.Errorf("extract: %w", err))
		}
		if selector == "" {
			s.rememberSnapshot(page, extracted)
		}

		// Build a PreviewResult equivalent from observer + extract so the
		// output shape stays consistent with the URL-provided path.
		errs := s.observerErrors()
		net := s.observerNetwork()
		pv := &engine.PreviewResult{
			PageInfo: &engine.PageInfo{URL: info.URL, Title: info.Title, Status: 200},
			Errors:   errs,
			Network:  net,
			DOM:      extracted,
			Summary: engine.PreviewSummary{
				TotalRequests:    len(net),
				FailedRequests:   countFailed(net),
				ErrorCount:       countByLevel(errs, "error"),
				WarningCount:     countByLevel(errs, "warning"),
				InteractiveCount: countInteractive(extracted),
			},
		}
		return previewResult(pv)
	})
}

func (s *Server) handleNavigate(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	url := mcpgo.ParseString(req, "url", "")
	if url == "" {
		return errResult(fmt.Errorf("url is required"))
	}
	wait := mcpgo.ParseString(req, "wait", "domcontentloaded")

	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		info, err := engine.Navigate(page, url, wait)
		if err != nil {
			recovered, recErr := tryNavigateRecovery(page, err)
			if !recovered {
				if recErr != nil {
					return errResult(fmt.Errorf("navigate failed and recovery failed: %w (orig: %v)", recErr, err))
				}
				return errResult(fmt.Errorf("navigate: %w", err))
			}
			if pi, infoErr := page.Info(); infoErr == nil && pi != nil {
				info = &engine.PageInfo{URL: pi.URL, Title: pi.Title, Status: 200}
			}
		}
		if s.opts.DismissCookies {
			if antibot.DismissCookieBanner(page) {
				_ = engine.WaitForPage(page, "stable")
			}
		}
		_ = b.InvalidateCachedExtract(page)
		summary := fmt.Sprintf("[%d] %s — %s (%dms)", info.Status, info.Title, info.URL, info.TimeMs)
		return jsonResult(info, summary)
	})
}

func (s *Server) handleClick(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	ref := normalizeRef(mcpgo.ParseString(req, "ref", ""))
	if ref == "" {
		return errResult(fmt.Errorf("ref is required"))
	}
	button, berr := interact.ParseMouseButton(mcpgo.ParseString(req, "button", "left"))
	if berr != nil {
		return errResult(fmt.Errorf("click: %w", berr))
	}
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		snap := s.snapshotForResolve(page)
		popupMark := engine.PopupMark(page)
		var err error
		if s.rt != nil {
			err = s.rt.PageSession(page).Click(ref, snap, button)
		} else {
			err = engine.ClickRefWithButton(page, ref, snap, button)
		}
		if err != nil {
			return errResult(fmt.Errorf("click %s: %w", ref, err))
		}
		if popup := engine.AdoptClickPopup(page, popupMark, snap, ref); popup != nil {
			s.adoptPage(b, popup)
			page = popup
		}
		return s.mutationResult(b, page, fmt.Sprintf("clicked %s", ref), snapshotModeFromReq(req))
	})
}

func (s *Server) handleType(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	ref := normalizeRef(mcpgo.ParseString(req, "ref", ""))
	text := mcpgo.ParseString(req, "text", "")
	submit := mcpgo.ParseBoolean(req, "submit", false)
	if ref == "" {
		return errResult(fmt.Errorf("ref is required"))
	}
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		snap := s.snapshotForResolve(page)
		var err error
		if s.rt != nil {
			err = s.rt.PageSession(page).Type(ref, text, snap)
		} else {
			err = engine.TypeRef(page, ref, text, snap)
		}
		if err != nil {
			return errResult(fmt.Errorf("type %s: %w", ref, err))
		}
		summary := fmt.Sprintf("typed into %s (%d chars)", ref, len(text))
		if submit {
			if err := engine.PressKey(page, "Enter", ref, snap); err != nil {
				return errResult(fmt.Errorf("submit %s: %w", ref, err))
			}
			summary += " + Enter"
		}
		return s.mutationResult(b, page, summary, snapshotModeFromReq(req))
	})
}

func (s *Server) handleSelect(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	ref := normalizeRef(mcpgo.ParseString(req, "ref", ""))
	value := mcpgo.ParseString(req, "value", "")
	if ref == "" || value == "" {
		return errResult(fmt.Errorf("ref and value are required"))
	}
	// Allow JSON array string for multi-select.
	values := []string{value}
	if strings.HasPrefix(value, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(value), &arr); err == nil && len(arr) > 0 {
			values = arr
		}
	}
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		snap := s.snapshotForResolve(page)
		var err error
		if s.rt != nil {
			err = s.rt.PageSession(page).Select(ref, values, snap)
		} else {
			err = engine.SelectOption(page, ref, values, snap)
		}
		if err != nil {
			return errResult(fmt.Errorf("select %s: %w", ref, err))
		}
		return s.mutationResult(b, page, fmt.Sprintf("selected %v in %s", values, ref), snapshotModeFromReq(req))
	})
}

func (s *Server) handlePress(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	key := mcpgo.ParseString(req, "key", "")
	if key == "" {
		return errResult(fmt.Errorf("key is required"))
	}
	ref := normalizeRef(mcpgo.ParseString(req, "ref", ""))
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		snap := s.snapshotForResolve(page)
		if err := engine.PressKey(page, key, ref, snap); err != nil {
			return errResult(fmt.Errorf("press %s: %w", key, err))
		}
		summary := fmt.Sprintf("pressed %s", key)
		if ref != "" {
			summary = fmt.Sprintf("pressed %s on %s", key, ref)
		}
		return s.mutationResult(b, page, summary)
	})
}

func (s *Server) handleWaitFor(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	ref := normalizeRef(mcpgo.ParseString(req, "ref", ""))
	selector := mcpgo.ParseString(req, "selector", "")
	text := mcpgo.ParseString(req, "text", "")
	timeoutMs := int(mcpgo.ParseFloat64(req, "timeout_ms", 5000))
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	if timeoutMs > 30000 {
		timeoutMs = 30000
	}
	urlNeedle := mcpgo.ParseString(req, "url", "")
	load := mcpgo.ParseString(req, "load", "")
	state := mcpgo.ParseString(req, "state", "")
	if ref == "" && selector == "" && text == "" && urlNeedle == "" && load == "" {
		select {
		case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		case <-ctx.Done():
			return errResult(ctx.Err())
		}
		return mcpgo.NewToolResultText(fmt.Sprintf("waited %dms (no condition)", timeoutMs)), nil
	}
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		start := time.Now()
		if _, err := engine.WaitForAgent(page, b, s.snapshotForResolve(page), engine.WaitSpec{
			Selector: selector,
			Ref:      ref,
			Text:     text,
			URL:      urlNeedle,
			Load:     load,
			State:    state,
			Timeout:  time.Duration(timeoutMs) * time.Millisecond,
		}, time.Duration(timeoutMs)*time.Millisecond); err != nil {
			return errResult(err)
		}
		label := ref
		if label == "" {
			label = selector
		}
		if label == "" {
			label = text
		}
		if label == "" {
			label = urlNeedle
		}
		if label == "" {
			label = load
		}
		return mcpgo.NewToolResultText(fmt.Sprintf("waited %dms for %q", time.Since(start).Milliseconds(), label)), nil
	})
}

func (s *Server) handleEval(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if err := s.opts.Policy.AllowAction("eval"); err != nil {
		return errResult(err)
	}
	expr := mcpgo.ParseString(req, "expression", "")
	if expr == "" {
		return errResult(fmt.Errorf("expression is required"))
	}
	ref := normalizeRef(mcpgo.ParseString(req, "ref", ""))
	timeoutMs := int(mcpgo.ParseFloat64(req, "timeout_ms", 8000))
	if timeoutMs <= 0 {
		timeoutMs = 8000
	}
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		snap := s.snapshotForResolve(page)
		value, err := engine.EvalJSTimeout(page, expr, ref, snap, time.Duration(timeoutMs)*time.Millisecond)
		if err != nil {
			return errResult(fmt.Errorf("eval: %w", err))
		}
		_ = b.InvalidateCachedExtract(page)
		return mcpgo.NewToolResultText(value), nil
	})
}

func (s *Server) handleScreenshot(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	ref := normalizeRef(mcpgo.ParseString(req, "ref", ""))
	fullPage := mcpgo.ParseBoolean(req, "full_page", false)
	format := mcpgo.ParseString(req, "format", "webp")
	quality := int(mcpgo.ParseFloat64(req, "quality", 60))
	annotate := mcpgo.ParseBoolean(req, "annotate", false)
	if quality < 1 || quality > 100 {
		quality = 60
	}
	if annotate {
		format = "png"
		quality = 0
	}
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		snap := s.snapshotForResolve(page)
		data, err := engine.TakeScreenshotFormat(page, fullPage, ref, format, quality, snap)
		if err != nil {
			return errResult(fmt.Errorf("screenshot: %w", err))
		}
		if annotate && snap != nil {
			data, err = overlay.AnnotateScreenshot(page, snap, data)
			if err != nil {
				return errResult(fmt.Errorf("annotate: %w", err))
			}
		}
		mime := "image/" + format
		summary := fmt.Sprintf("captured %s (%d bytes)", format, len(data))
		if annotate {
			summary += " (annotated)"
		}
		return mcpgo.NewToolResultImage(summary, base64.StdEncoding.EncodeToString(data), mime), nil
	})
}

func (s *Server) handleHover(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	ref := normalizeRef(mcpgo.ParseString(req, "ref", ""))
	if ref == "" {
		return errResult(fmt.Errorf("ref is required"))
	}
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		snap := s.snapshotForResolve(page)
		var err error
		if s.rt != nil {
			err = s.rt.PageSession(page).Hover(ref, snap)
		} else {
			err = engine.HoverRef(page, ref, snap)
		}
		if err != nil {
			return errResult(fmt.Errorf("hover %s: %w", ref, err))
		}
		return s.mutationResult(b, page, fmt.Sprintf("hovered %s", ref), snapshotModeFromReq(req))
	})
}

func (s *Server) handleDrag(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	from := normalizeRef(mcpgo.ParseString(req, "from", ""))
	to := normalizeRef(mcpgo.ParseString(req, "to", ""))
	steps := int(mcpgo.ParseFloat64(req, "steps", 10))
	if from == "" || to == "" {
		return errResult(fmt.Errorf("from and to refs are required"))
	}
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		snap := s.snapshotForResolve(page)
		if err := engine.DragDrop(page, from, to, snap, steps); err != nil {
			return errResult(fmt.Errorf("drag %s→%s: %w", from, to, err))
		}
		return s.mutationResult(b, page, fmt.Sprintf("dragged %s -> %s", from, to))
	})
}

func (s *Server) handleSwipe(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	fromX := mcpgo.ParseFloat64(req, "from_x", -1)
	fromY := mcpgo.ParseFloat64(req, "from_y", -1)
	toX := mcpgo.ParseFloat64(req, "to_x", -1)
	toY := mcpgo.ParseFloat64(req, "to_y", -1)
	if fromX < 0 || fromY < 0 || toX < 0 || toY < 0 {
		return errResult(fmt.Errorf("from_x, from_y, to_x and to_y are required and must be >= 0"))
	}
	durationMs := int(mcpgo.ParseFloat64(req, "duration_ms", 300))
	if durationMs <= 0 {
		durationMs = 300
	}
	if durationMs > 10000 {
		durationMs = 10000
	}
	steps := int(mcpgo.ParseFloat64(req, "steps", 12))
	if steps <= 0 {
		steps = 12
	}
	if steps > 100 {
		steps = 100
	}
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		// Chrome drops synthesized touch events on a page whose widget has no
		// touch support, so a swipe on a non-emulated tab would silently do
		// nothing. Turn touch on rather than failing.
		hint := ""
		if !s.emulation.Touch {
			if err := engine.EnsureTouchEmulation(page); err != nil {
				return errResult(fmt.Errorf("swipe: %w", err))
			}
			s.emulation.Touch = true
			hint = " (touch emulation enabled for this swipe)"
		}
		if err := engine.SwipeTouch(page, fromX, fromY, toX, toY, steps, time.Duration(durationMs)*time.Millisecond); err != nil {
			return errResult(fmt.Errorf("swipe: %w", err))
		}
		summary := fmt.Sprintf("swiped (%.0f,%.0f) -> (%.0f,%.0f) in %dms%s", fromX, fromY, toX, toY, durationMs, hint)
		return s.mutationResult(b, page, summary, snapshotModeFromReq(req))
	})
}

func (s *Server) handleEmulate(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	reset := mcpgo.ParseBoolean(req, "reset", false)
	device := strings.TrimSpace(mcpgo.ParseString(req, "device", ""))
	width := int(mcpgo.ParseFloat64(req, "width", 0))
	height := int(mcpgo.ParseFloat64(req, "height", 0))
	dpr := mcpgo.ParseFloat64(req, "device_scale_factor", 0)
	userAgent := strings.TrimSpace(mcpgo.ParseString(req, "user_agent", ""))
	colorScheme := strings.TrimSpace(mcpgo.ParseString(req, "color_scheme", ""))
	// mobile/touch are tri-state: absent means "keep whatever the preset or the
	// current profile says", which a plain ParseBoolean default cannot express.
	hasMobile := hasArg(req, "mobile")
	mobile := mcpgo.ParseBoolean(req, "mobile", false)
	hasTouch := hasArg(req, "touch")
	touch := mcpgo.ParseBoolean(req, "touch", false)

	if reset {
		if device != "" || width > 0 || height > 0 || dpr > 0 || userAgent != "" || colorScheme != "" || hasMobile || hasTouch {
			return errResult(fmt.Errorf("reset cannot be combined with other emulation parameters"))
		}
		return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
			if err := engine.ResetEmulation(page); err != nil {
				return errResult(fmt.Errorf("emulate reset: %w", err))
			}
			s.emulation = engine.EmulationState{}
			_ = b.ClearEmulationState()
			_ = b.InvalidateCachedExtract(page)
			return mcpgo.NewToolResultText("emulation reset: real viewport, no touch, browser user-agent. Re-snapshot before using refs."), nil
		})
	}

	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		state := s.emulation
		if device != "" {
			preset, ok := engine.DeviceByName(device)
			if !ok {
				return errResult(fmt.Errorf("unknown device %q (iphone-se, iphone-14, iphone-14-pro, iphone-14-pro-max, pixel-7, pixel-8-pro, ipad, ipad-pro, desktop, desktop-2k)", device))
			}
			next := engine.EmulationFromDevice(preset)
			// Desktop presets carry no UA of their own; keep whatever is in force
			// rather than silently reverting to the browser default mid-flow.
			if next.UserAgent == "" {
				next.UserAgent = state.UserAgent
			}
			next.ColorScheme = state.ColorScheme
			next.Timezone = state.Timezone
			state = next
		}
		if width > 0 {
			state.Width = width
		}
		if height > 0 {
			state.Height = height
		}
		if dpr > 0 {
			state.DPR = dpr
		}
		if hasMobile {
			state.Mobile = mobile
		}
		if hasTouch {
			state.Touch = touch
		}
		if userAgent != "" {
			state.UserAgent = userAgent
		}
		if colorScheme != "" {
			state.ColorScheme = colorScheme
		}
		if device == "" && (width > 0 || height > 0 || dpr > 0 || hasMobile || hasTouch) {
			// A geometry axis was overridden by hand, so the preset label the
			// profile still carries would lie about what the page actually sees.
			state.Device = ""
		}

		if state.Empty() {
			return errResult(fmt.Errorf("nothing to emulate: pass device, width+height, mobile, touch, user_agent, color_scheme, or reset=true"))
		}
		if (state.Width > 0) != (state.Height > 0) {
			return errResult(fmt.Errorf("width and height must be given together"))
		}
		if state.Width < 0 || state.Width > 10000 || state.Height < 0 || state.Height > 10000 {
			return errResult(fmt.Errorf("width and height must be between 1 and 10000 CSS pixels"))
		}
		if state.DPR < 0 || state.DPR > 5 {
			return errResult(fmt.Errorf("device_scale_factor must be between 0.1 and 5"))
		}
		if state.Width > 0 && state.DPR <= 0 {
			state.DPR = 1
		}

		if err := engine.ApplyEmulationProfile(page, state); err != nil {
			return errResult(fmt.Errorf("emulate: %w", err))
		}
		s.emulation = state
		// No-op outside a managed session; keeps a named session consistent
		// with what the CLI would have persisted.
		_ = b.SetEmulationState(state)
		_ = b.InvalidateCachedExtract(page)
		return jsonResult(state, "emulating "+state.Summary()+" — re-snapshot before using refs")
	})
}

func (s *Server) handleFillForm(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	fieldsRaw := mcpgo.ParseString(req, "fields", "")
	if fieldsRaw == "" {
		return errResult(fmt.Errorf("fields is required"))
	}
	var fields map[string]string
	if err := json.Unmarshal([]byte(fieldsRaw), &fields); err != nil {
		return errResult(fmt.Errorf("fields must be a JSON object: %w", err))
	}
	normalized := make(map[string]string, len(fields))
	for ref, value := range fields {
		normalized[normalizeRef(ref)] = value
	}
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		snap := s.snapshotForResolve(page)
		filled, snap, err := engine.FillFields(b, page, normalized, snap)
		if err != nil {
			return errResult(err)
		}
		if snap != nil {
			s.snapshot = snap
		}
		return s.mutationResult(b, page, fmt.Sprintf("filled %d fields", filled))
	})
}

func (s *Server) handleUpload(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if err := s.opts.Policy.AllowAction("upload"); err != nil {
		return errResult(err)
	}
	ref := normalizeRef(mcpgo.ParseString(req, "ref", ""))
	pathsRaw := mcpgo.ParseString(req, "paths", "")
	if ref == "" || pathsRaw == "" {
		return errResult(fmt.Errorf("ref and paths are required"))
	}
	var paths []string
	if strings.HasPrefix(pathsRaw, "[") {
		if err := json.Unmarshal([]byte(pathsRaw), &paths); err != nil {
			paths = []string{pathsRaw}
		}
	} else {
		paths = []string{pathsRaw}
	}
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		snap := s.snapshotForResolve(page)
		el, err := engine.ResolveRef(page, ref, snap)
		if err != nil {
			return errResult(fmt.Errorf("upload %s: %w", ref, err))
		}
		if err := el.SetFiles(paths); err != nil {
			return errResult(fmt.Errorf("upload: %w", err))
		}
		return s.mutationResult(b, page, fmt.Sprintf("uploaded %d file(s) to %s", len(paths), ref))
	})
}

func (s *Server) adoptPage(b *engine.Browser, page *rod.Page) {
	_ = b.SetCurrentPage(page)
	s.page = page
	s.snapshot = b.Snapshot(page)
	if s.dialogPolicy == nil {
		s.dialogPolicy = &engine.DialogAutoPolicy{Accept: true}
	}
	engine.StartDialogAutoHandler(page, s.dialogPolicy)
	if s.rt == nil {
		s.rt = engine.NewRuntime(b)
	}
	if !s.opts.Stealth {
		if hub := s.rt.AttachEvents(page); hub != nil {
			s.observer = hub.Observer()
		}
	}
	// A popup or a new tab is a fresh target with no emulation override.
	s.replayEmulationLocked(page)
}
func (s *Server) handleTabs(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	action := mcpgo.ParseString(req, "action", "list")
	index := int(mcpgo.ParseFloat64(req, "index", -1))

	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		currentID := string(page.TargetID)
		switch action {
		case "switch":
			if index < 0 {
				return errResult(fmt.Errorf("index is required for switch"))
			}
			newPage, err := engine.SwitchTab(b.RodBrowser(), index)
			if err != nil {
				return errResult(fmt.Errorf("switch tab: %w", err))
			}
			s.adoptPage(b, newPage)
			info, _ := newPage.Info()
			url := ""
			if info != nil {
				url = info.URL
			}
			return mcpgo.NewToolResultText(fmt.Sprintf("switched to tab %d: %s", index, url)), nil
		case "close":
			if index < 0 {
				return errResult(fmt.Errorf("index is required for close"))
			}
			closedID, err := engine.CloseTab(b.RodBrowser(), index)
			if err != nil {
				return errResult(fmt.Errorf("close tab: %w", err))
			}
			_ = b.DeleteSnapshot(closedID)
			if s.page != nil && s.page.TargetID == closedID {
				pages, perr := b.RodBrowser().Pages()
				if perr == nil && len(pages) > 0 {
					s.adoptPage(b, pages[0])
				} else {
					s.page = nil
					s.snapshot = nil
				}
			}
			return mcpgo.NewToolResultText(fmt.Sprintf("closed tab %d", index)), nil
		case "new":
			url := mcpgo.ParseString(req, "url", "")
			newPage, err := engine.NewTab(b.RodBrowser(), url)
			if err != nil {
				return errResult(fmt.Errorf("new tab: %w", err))
			}
			s.adoptPage(b, newPage)
			info, _ := newPage.Info()
			label := "about:blank"
			if info != nil && info.URL != "" {
				label = info.URL
			}
			return mcpgo.NewToolResultText(fmt.Sprintf("opened tab: %s", label)), nil
		default:
			tabs, err := engine.ListTabs(b.RodBrowser(), currentID)
			if err != nil {
				return errResult(fmt.Errorf("list tabs: %w", err))
			}
			return jsonResult(tabs, fmt.Sprintf("%d tabs open", len(tabs)))
		}
	})
}

func (s *Server) handleDialog(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	action := strings.ToLower(mcpgo.ParseString(req, "action", "accept"))
	text := mcpgo.ParseString(req, "text", "")
	// Dialog events are handled by a background CDP listener while MCP calls
	// may reconfigure the policy concurrently. Protect both the pointer and
	// the policy update; the policy itself also synchronizes its fields.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dialogPolicy == nil {
		s.dialogPolicy = &engine.DialogAutoPolicy{}
	}
	switch action {
	case "dismiss":
		s.dialogPolicy.Set(false, text)
	default:
		s.dialogPolicy.Set(true, text)
		action = "accept"
	}
	return mcpgo.NewToolResultText(fmt.Sprintf("dialogs will %s", action)), nil
}
func (s *Server) handleBack(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		res, err := historyStep(page, "back")
		if err == nil {
			_ = b.InvalidateCachedExtract(page)
		}
		return res, err
	})
}

func (s *Server) handleForward(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return s.withPage(ctx, func(b *engine.Browser, page *rod.Page) (*mcpgo.CallToolResult, error) {
		res, err := historyStep(page, "forward")
		if err == nil {
			_ = b.InvalidateCachedExtract(page)
		}
		return res, err
	})
}

// historyStep mirrors cmd/back.go: trigger the history step, wait until the
// page is stable, then report the resulting URL. WaitForPage("stable") is
// what the CLI uses — pre-registering the lifecycle listener inside
// engine.Navigate isn't an option here because the navigation kick is one
// method call, not a separate page.Navigate(url).
func historyStep(page *rod.Page, action string) (*mcpgo.CallToolResult, error) {
	delta := 1
	if action == "back" {
		delta = -1
	}
	if err := engine.HistoryStep(page, delta, "stable"); err != nil {
		return errResult(fmt.Errorf("%s: %w", action, err))
	}
	info, err := page.Info()
	if err != nil || info == nil {
		return mcpgo.NewToolResultText("navigated " + action), nil
	}
	return mcpgo.NewToolResultText(fmt.Sprintf("navigated %s to %s", action, info.URL)), nil
}

// ============================================================================
// helpers (private)
// ============================================================================

// previewResult shapes a PreviewResult into an MCP text content with a
// one-line human header + structured JSON. Matches the existing CLI output
// style so refs look identical whether the agent uses CLI or MCP.
func previewResult(pv *engine.PreviewResult) (*mcpgo.CallToolResult, error) {
	if pv == nil || pv.PageInfo == nil {
		return mcpgo.NewToolResultError("preview: empty result"), nil
	}
	header := fmt.Sprintf("[%d] %s — %s | %d errors, %d failed reqs, %d interactive",
		pv.PageInfo.Status, pv.PageInfo.Title, pv.PageInfo.URL,
		pv.Summary.ErrorCount, pv.Summary.FailedRequests, pv.Summary.InteractiveCount)
	data, err := json.Marshal(pv)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("marshal: %v", err)), nil
	}
	return mcpgo.NewToolResultText(header + "\n" + string(data)), nil
}

// jsonResult marshals payload as JSON and emits an MCP text result that
// holds both a human summary and the raw JSON.
func jsonResult(payload any, summary string) (*mcpgo.CallToolResult, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("marshal: %v", err)), nil
	}
	if summary == "" {
		return mcpgo.NewToolResultText(string(data)), nil
	}
	return mcpgo.NewToolResultText(summary + "\n" + string(data)), nil
}

// hasArg reports whether the caller actually supplied a key, as opposed to the
// tool schema carrying a default for it. Needed for tri-state booleans where
// "absent" and "false" must behave differently.
func hasArg(req mcpgo.CallToolRequest, key string) bool {
	args := req.GetArguments()
	if args == nil {
		return false
	}
	v, ok := args[key]
	return ok && v != nil
}

// normalizeRef accepts "@3", "3", "ref:3" and returns "@3" (or empty).
func normalizeRef(in string) string {
	r := strings.TrimSpace(in)
	if r == "" {
		return ""
	}
	r = strings.TrimPrefix(r, "ref:")
	r = strings.TrimPrefix(r, "ref-")
	if !strings.HasPrefix(r, "@") {
		r = "@" + r
	}
	return r
}

// observerErrors returns errors accumulated by the long-lived observer.
// Maps ObserverEvent → ErrorEntry so handleSnapshot can build a result
// that matches engine.PreviewResult's shape exactly.
func (s *Server) observerErrors() []engine.ErrorEntry {
	if s.observer == nil {
		return nil
	}
	events := s.observer.Drain(0)
	out := make([]engine.ErrorEntry, 0, len(events))
	for _, e := range events {
		switch e.Kind {
		case engine.KindError, engine.KindConsole:
			if e.Level == "error" || e.Level == "warning" {
				out = append(out, engine.ErrorEntry{
					Type:    "console",
					Level:   e.Level,
					Message: e.Text,
					Source:  e.Source,
					TimeMs:  e.TS,
				})
			}
		}
	}
	return out
}

// observerNetwork returns network entries from the long-lived observer.
func (s *Server) observerNetwork() []engine.NetworkEntry {
	if s.observer == nil {
		return nil
	}
	events := s.observer.Drain(0)
	out := make([]engine.NetworkEntry, 0)
	for _, e := range events {
		if e.Kind != engine.KindNet {
			continue
		}
		out = append(out, engine.NetworkEntry{
			Method:   e.Method,
			URL:      e.URL,
			Status:   e.Status,
			Size:     int(e.Size),
			TimeMs:   e.DurationMs,
			MimeType: e.MimeType,
			Error:    e.Failed,
		})
	}
	return out
}

func countFailed(net []engine.NetworkEntry) int {
	n := 0
	for _, e := range net {
		if e.Status >= 400 || e.Error != "" {
			n++
		}
	}
	return n
}

func countByLevel(errs []engine.ErrorEntry, level string) int {
	n := 0
	for _, e := range errs {
		if e.Level == level {
			n++
		}
	}
	return n
}

func countInteractive(result *engine.ExtractionResult) int {
	if result == nil {
		return 0
	}
	return result.Stats.InteractiveCount
}

// tryNavigateRecovery — minimal version of the old retry hook. If the
// navigate failed with a deadline exceeded mid-anti-bot challenge, give
// the page 5s to settle and probe page.Info() once before giving up.
func tryNavigateRecovery(page *rod.Page, err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	msg := err.Error()
	if !strings.Contains(msg, "deadline") && !strings.Contains(msg, "timeout") {
		return false, nil
	}
	time.Sleep(5 * time.Second)
	if info, err := page.Info(); err == nil && info != nil && info.URL != "" {
		return true, nil
	}
	return false, nil
}

func snapshotModeFromReq(req mcpgo.CallToolRequest) engine.SnapshotMode {
	mode, err := engine.ParseSnapshotMode(mcpgo.ParseString(req, "snapshot", "diff"))
	if err != nil {
		return engine.SnapshotModeDiff
	}
	return mode
}

func (s *Server) mutationResult(b *engine.Browser, page *rod.Page, summary string, mode ...engine.SnapshotMode) (*mcpgo.CallToolResult, error) {
	chosen := engine.SnapshotModeDiff
	if len(mode) > 0 && mode[0] != "" {
		chosen = mode[0]
	}
	prev := s.snapshotForResolve(page)
	switch chosen {
	case engine.SnapshotModeNone:
		_ = b.InvalidateCachedExtract(page)
		return jsonResult(engine.SnapshotDiff{Unchanged: true}, summary)
	case engine.SnapshotModeFull:
		_ = b.InvalidateCachedExtract(page)
		if err := engine.WaitForImminentDOM(page, 0); err != nil {
			return errResult(err)
		}
		result, err := engine.Extract(page, engine.LevelSkeleton, "", false)
		if err != nil {
			return errResult(err)
		}
		s.rememberSnapshot(page, result)
		return jsonResult(result, summary)
	default:
		diff, result, err := engine.CaptureMutation(b, page, prev)
		if err != nil {
			return errResult(err)
		}
		if result != nil {
			s.rememberSnapshot(page, result)
		}
		return jsonResult(diff, summary+"\n"+engine.FormatDiff(diff))
	}
}
