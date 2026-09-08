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
	"sort"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/antibot"
	"github.com/dev-toolings/ghostchrome/internal/core/interact"
	"github.com/dev-toolings/ghostchrome/internal/core/overlay"
	"github.com/dev-toolings/ghostchrome/internal/runtime"
	"github.com/go-rod/rod"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
)

// toolHandler is the method-expression form of a tool handler, so the
// registration table can be built without a *Server in hand.
type toolHandler func(*Server, context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error)

// toolDef is one entry of the MCP tool table.
type toolDef struct {
	tool    mcpgo.Tool
	handler toolHandler
}

// registerTools wires every tool in toolDefs onto the MCP server.
func registerTools(srv *mcpsrv.MCPServer, s *Server) {
	for _, def := range toolDefs() {
		h := def.handler
		srv.AddTool(def.tool, func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			return h(s, ctx, req)
		})
	}
}

// Tools returns the sorted names of the tools this surface registers. It is
// derived from toolDefs, so a parity test reads the truth instead of a
// hand-maintained copy — the MCP counterpart of runtime.Ops().
//
// Build-tagged recipes may append to ExtraToolRegistrars; those are outside
// the canonical catalog and deliberately not reported here.
func Tools() []string {
	defs := toolDefs()
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.tool.Name)
	}
	sort.Strings(names)
	return names
}

// ============================================================================
// handlers
// ============================================================================
//
// Every browser verb below routes through internal/runtime: the op layer owns
// ref resolution, the recovery chain and mutation diffing, and this file owns
// the MCP argument parsing and the text rendering the agent reads. What stays
// on a direct engine call is either an MCP-only concern with no JSONL
// counterpart (emulate, swipe geometry, the Preview composite) or a value
// fetch, never a second implementation of a shared verb.

// dispatch runs one op through the shared op layer, which withPage has already
// bound to this request's page. Argument names are the JSONL ones on purpose:
// they are the frozen contract both SDKs are typed against.
func (s *Server) dispatch(op string, args map[string]any) (any, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("%s: encode args: %w", op, err)
	}
	return s.ops.Dispatch(op, raw)
}

func (s *Server) handleSnapshot(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	url := mcpgo.ParseString(req, "url", "")
	wait := mcpgo.ParseString(req, "wait", "domcontentloaded")
	levelStr := mcpgo.ParseString(req, "level", "content")
	selector := mcpgo.ParseString(req, "selector", "")

	level := engine.ExtractLevel(levelStr)
	if err := engine.ValidateExtractLevel(level); err != nil {
		return errResult(err)
	}

	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		// With URL: engine.Preview is the single navigate+observe+extract pass
		// that gives this MCP-only tool its reason to exist (one call instead of
		// three, one set of tokens instead of three). Composing runtime ops here
		// would report a different errors/network set, so the composite stays one
		// engine call — but the ref table it produces is handed straight to the
		// op layer, which owns every ref an agent will use next.
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
				_ = s.ops.RememberExtraction(pv.DOM)
			}
			return previewResult(pv)
		}

		// Without URL: the "extract" op does the extraction and the ref-table
		// bookkeeping. Errors/network come from the long-lived observer
		// (started in ensurePageLocked).
		info, err := page.Info()
		if err != nil {
			return errResult(fmt.Errorf("page info: %w", err))
		}
		out, err := s.dispatch("extract", map[string]any{"level": levelStr, "selector": selector})
		if err != nil {
			return errResult(fmt.Errorf("extract: %w", err))
		}
		extracted, _ := out.(*engine.ExtractionResult)

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

	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		out, err := s.dispatch("navigate", map[string]any{"url": url, "wait": wait})
		info, _ := out.(*engine.PageInfo)
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
		if info == nil {
			return errResult(fmt.Errorf("navigate: no page info"))
		}
		if s.opts.DismissCookies {
			if antibot.DismissCookieBanner(page) {
				_ = engine.WaitForPage(page, "stable")
			}
		}
		// The op already invalidated once; dismissing a banner mutates the DOM
		// after that, so the cache has to be dropped again.
		_ = s.browser.InvalidateCachedExtract(page)
		summary := fmt.Sprintf("[%d] %s — %s (%dms)", info.Status, info.Title, info.URL, info.TimeMs)
		return jsonResult(info, summary)
	})
}

func (s *Server) handleClick(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	ref := normalizeRef(mcpgo.ParseString(req, "ref", ""))
	if ref == "" {
		return errResult(fmt.Errorf("ref is required"))
	}
	button := mcpgo.ParseString(req, "button", "left")
	if _, berr := interact.ParseMouseButton(button); berr != nil {
		return errResult(fmt.Errorf("click: %w", berr))
	}
	mode := snapshotModeFromReq(req)
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		out, err := s.dispatch("click", map[string]any{"ref": ref, "button": button, "snapshot": string(mode)})
		if err != nil {
			return errResult(fmt.Errorf("click %s: %w", ref, err))
		}
		return mutationResult(mode, out, fmt.Sprintf("clicked %s", ref))
	})
}

func (s *Server) handleType(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	ref := normalizeRef(mcpgo.ParseString(req, "ref", ""))
	text := mcpgo.ParseString(req, "text", "")
	submit := mcpgo.ParseBoolean(req, "submit", false)
	if ref == "" {
		return errResult(fmt.Errorf("ref is required"))
	}
	mode := snapshotModeFromReq(req)
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		out, err := s.dispatch("type", map[string]any{
			"ref": ref, "text": text, "submit": submit, "snapshot": string(mode),
		})
		if err != nil {
			return errResult(fmt.Errorf("type %s: %w", ref, err))
		}
		summary := fmt.Sprintf("typed into %s (%d chars)", ref, len(text))
		if submit {
			summary += " + Enter"
		}
		return mutationResult(mode, out, summary)
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
	mode := snapshotModeFromReq(req)
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		out, err := s.dispatch("select", map[string]any{"ref": ref, "values": values, "snapshot": string(mode)})
		if err != nil {
			return errResult(fmt.Errorf("select %s: %w", ref, err))
		}
		return mutationResult(mode, out, fmt.Sprintf("selected %v in %s", values, ref))
	})
}

func (s *Server) handlePress(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	key := mcpgo.ParseString(req, "key", "")
	if key == "" {
		return errResult(fmt.Errorf("key is required"))
	}
	ref := normalizeRef(mcpgo.ParseString(req, "ref", ""))
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		out, err := s.dispatch("press", map[string]any{"key": key, "ref": ref})
		if err != nil {
			return errResult(fmt.Errorf("press %s: %w", key, err))
		}
		summary := fmt.Sprintf("pressed %s", key)
		if ref != "" {
			summary = fmt.Sprintf("pressed %s on %s", key, ref)
		}
		return mutationResult(engine.SnapshotModeDiff, out, summary)
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
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		start := time.Now()
		// wait_for is the MCP-only extension of the JSONL "wait" op: same
		// conditions, plus the clamped timeout above and the pure-delay branch.
		if _, err := s.dispatch("wait", map[string]any{
			"ref":        ref,
			"selector":   selector,
			"text":       text,
			"url":        urlNeedle,
			"load":       load,
			"state":      state,
			"timeout_ms": timeoutMs,
		}); err != nil {
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
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		// EvalTimeout is the op layer's eval with the per-call deadline this
		// tool exposes and the JSONL op does not.
		value, err := s.ops.EvalTimeout(expr, ref, time.Duration(timeoutMs)*time.Millisecond)
		if err != nil {
			return errResult(fmt.Errorf("eval: %w", err))
		}
		_ = s.browser.InvalidateCachedExtract(page)
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
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		data, err := s.ops.CaptureScreenshot(runtime.ScreenshotSpec{
			FullPage: fullPage,
			Ref:      ref,
			Format:   format,
			Quality:  quality,
		})
		if err != nil {
			return errResult(fmt.Errorf("screenshot: %w", err))
		}
		if snap := s.refs(); annotate && snap != nil {
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
	mode := snapshotModeFromReq(req)
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		out, err := s.dispatch("hover", map[string]any{"ref": ref, "snapshot": string(mode)})
		if err != nil {
			return errResult(fmt.Errorf("hover %s: %w", ref, err))
		}
		return mutationResult(mode, out, fmt.Sprintf("hovered %s", ref))
	})
}

func (s *Server) handleDrag(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	from := normalizeRef(mcpgo.ParseString(req, "from", ""))
	to := normalizeRef(mcpgo.ParseString(req, "to", ""))
	steps := int(mcpgo.ParseFloat64(req, "steps", 10))
	if from == "" || to == "" {
		return errResult(fmt.Errorf("from and to refs are required"))
	}
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		prev := s.refs()
		// No JSONL "drag" op exists; Session.Drag is the shared implementation
		// so both refs resolve through the op layer's table and recovery.
		if err := s.ops.Drag(from, to, steps); err != nil {
			return errResult(fmt.Errorf("drag %s→%s: %w", from, to, err))
		}
		out, err := s.ops.Mutation(prev, engine.SnapshotModeDiff)
		if err != nil {
			return errResult(err)
		}
		return mutationResult(engine.SnapshotModeDiff, out, fmt.Sprintf("dragged %s -> %s", from, to))
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
	mode := snapshotModeFromReq(req)
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
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
		prev := s.refs()
		// MCP-only gesture: coordinates, not refs, so there is no JSONL op to
		// delegate to. Only the post-action observation is shared.
		if err := engine.SwipeTouch(page, fromX, fromY, toX, toY, steps, time.Duration(durationMs)*time.Millisecond); err != nil {
			return errResult(fmt.Errorf("swipe: %w", err))
		}
		out, err := s.ops.Mutation(prev, mode)
		if err != nil {
			return errResult(err)
		}
		summary := fmt.Sprintf("swiped (%.0f,%.0f) -> (%.0f,%.0f) in %dms%s", fromX, fromY, toX, toY, durationMs, hint)
		return mutationResult(mode, out, summary)
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
		return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
			if err := engine.ResetEmulation(page); err != nil {
				return errResult(fmt.Errorf("emulate reset: %w", err))
			}
			s.emulation = engine.EmulationState{}
			_ = s.browser.ClearEmulationState()
			_ = s.browser.InvalidateCachedExtract(page)
			return mcpgo.NewToolResultText("emulation reset: real viewport, no touch, browser user-agent. Re-snapshot before using refs."), nil
		})
	}

	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
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
		_ = s.browser.SetEmulationState(state)
		_ = s.browser.InvalidateCachedExtract(page)
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
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		out, err := s.dispatch("fill", map[string]any{"fields": normalized})
		if err != nil {
			return errResult(err)
		}
		filled := 0
		if counts, ok := out.(map[string]int); ok {
			filled = counts["filled"]
		}
		// Baseline taken after the op, not before: the "fill" op re-extracts
		// while resolving the fields and leaves a fresh ref table behind, so the
		// diff reported here is what changed once every field was written.
		prev := s.refs()
		// The JSONL "fill" op reports a count; this tool also reports the
		// resulting a11y diff, which is the shared post-action observation.
		diff, err := s.ops.Mutation(prev, engine.SnapshotModeDiff)
		if err != nil {
			return errResult(err)
		}
		return mutationResult(engine.SnapshotModeDiff, diff, fmt.Sprintf("filled %d fields", filled))
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
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		prev := s.refs()
		// No JSONL "upload" op; only the ref resolution is shared, because a
		// file input has to be handed to SetFiles as a live element.
		el, err := s.ops.ResolveRef(ref)
		if err != nil {
			return errResult(fmt.Errorf("upload %s: %w", ref, err))
		}
		if err := el.SetFiles(paths); err != nil {
			return errResult(fmt.Errorf("upload: %w", err))
		}
		out, err := s.ops.Mutation(prev, engine.SnapshotModeDiff)
		if err != nil {
			return errResult(err)
		}
		return mutationResult(engine.SnapshotModeDiff, out, fmt.Sprintf("uploaded %d file(s) to %s", len(paths), ref))
	})
}

func (s *Server) handleTabs(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	action := mcpgo.ParseString(req, "action", "list")
	index := int(mcpgo.ParseFloat64(req, "index", -1))

	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		args := map[string]any{"action": action}
		switch action {
		case "switch", "close":
			if index < 0 {
				return errResult(fmt.Errorf("index is required for %s", action))
			}
			args["index"] = index
		case "new":
			args["url"] = mcpgo.ParseString(req, "url", "")
		default:
			// Anything the schema enum does not cover has always listed.
			action = "list"
			args["action"] = "list"
		}
		out, err := s.dispatch("tabs", args)
		if err != nil {
			return errResult(err)
		}
		switch action {
		case "switch":
			return mcpgo.NewToolResultText(fmt.Sprintf("switched to tab %d: %s", index, tabURL(out))), nil
		case "close":
			return mcpgo.NewToolResultText(fmt.Sprintf("closed tab %d", index)), nil
		case "new":
			label := tabURL(out)
			if label == "" {
				label = "about:blank"
			}
			return mcpgo.NewToolResultText(fmt.Sprintf("opened tab: %s", label)), nil
		default:
			tabs, _ := out.([]engine.TabInfo)
			return jsonResult(tabs, fmt.Sprintf("%d tabs open", len(tabs)))
		}
	})
}

func (s *Server) handleDialog(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	action := strings.ToLower(mcpgo.ParseString(req, "action", "accept"))
	text := mcpgo.ParseString(req, "text", "")
	// Deliberately not routed through the op layer: this tool must not open
	// Chrome. It mutates the very policy object bindOps hands to the op layer,
	// so a policy set before the first navigation is the one an adopted popup
	// inherits.
	//
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
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		res, err := historyStep(page, "back")
		if err == nil {
			_ = s.browser.InvalidateCachedExtract(page)
		}
		return res, err
	})
}

func (s *Server) handleForward(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return s.withPage(ctx, func(page *rod.Page) (*mcpgo.CallToolResult, error) {
		res, err := historyStep(page, "forward")
		if err == nil {
			_ = s.browser.InvalidateCachedExtract(page)
		}
		return res, err
	})
}

// historyStep mirrors cmd/back.go: trigger the history step, wait until the
// page is stable, then report the resulting URL.
//
// Deliberately NOT routed through the JSONL "back"/"forward" ops: those wait
// for "load", this waits for "stable", and the JSONL ops take no arguments, so
// sharing them would mean either changing MCP's wait strategy or adding an
// argument to a frozen contract. WaitForPage("stable") is what the CLI uses —
// pre-registering the lifecycle listener inside engine.Navigate isn't an option
// here because the navigation kick is one method call, not a separate
// page.Navigate(url).
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

// mutationResult renders what the op layer returns after a mutation: a
// SnapshotDiff in diff mode (with the human diff appended), the skeleton
// ExtractionResult in full mode, the unchanged marker in none mode. The
// payload is emitted as-is — computing it is internal/runtime's job.
func mutationResult(mode engine.SnapshotMode, out any, summary string) (*mcpgo.CallToolResult, error) {
	if mode == engine.SnapshotModeDiff {
		diff, _ := out.(engine.SnapshotDiff)
		return jsonResult(out, summary+"\n"+engine.FormatDiff(diff))
	}
	return jsonResult(out, summary)
}

// tabURL reads the url a tabs op reports for the target it switched to or
// opened. Empty when the target had no metadata yet.
func tabURL(out any) string {
	m, ok := out.(map[string]any)
	if !ok {
		return ""
	}
	url, _ := m["url"].(string)
	return url
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
