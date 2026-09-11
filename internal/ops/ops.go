// Package ops defines the canonical catalog of ghostchrome agent operations.
//
// This is the single source of truth for op names, summaries, argument shapes,
// and which surfaces expose each op. The three live surfaces are:
//
//   - "jsonl" — internal/runtime dispatch table, framed by cmd/agent.go
//   - "mcp"   — internal/surface/mcp (tool registration)
//   - "ai"    — internal/surface/ai (ToolSpecs)
//
// The catalog is EXECUTABLE, not documentation: `go generate ./internal/ops/...`
// emits, from Catalog() alone,
//
//   - contracts/commands.json            — the frozen SDK contract
//   - internal/runtime/handlers_gen.go   — the JSONL name -> method bindings
//   - internal/surface/mcp/tools_gen.go  — the MCP tool schemas + handler pairs
//   - internal/surface/ai/tools_gen.go   — the AI tool specs
//
// Adding an op therefore means editing this file and running go generate. The
// generated bindings reference a hand-written handler method, so the build
// fails until the behaviour is written; registration can no longer drift from
// the catalog because no surface declares its own list any more.
//
// What the catalog does NOT own: handler bodies. Args/Summary describe the
// contract, MCP/AI describe the wire; the behaviour behind them stays hand
// written in internal/runtime and internal/surface/mcp.
//
//go:generate go run ./cmd/gen/main.go
package ops

// ArgType describes the JSON type of an argument.
type ArgType string

const (
	ArgString  ArgType = "string"
	ArgInteger ArgType = "integer"
	ArgNumber  ArgType = "number"
	ArgBoolean ArgType = "boolean"
	ArgArray   ArgType = "array"
	ArgObject  ArgType = "object"
)

// Arg describes one argument accepted by an op.
type Arg struct {
	Name     string  `json:"name"`
	Type     ArgType `json:"type"`
	Required bool    `json:"required"`
	// Description is a short human-readable note (optional).
	Description string `json:"description,omitempty"`
}

// SurfaceArg is one argument exactly as a single surface puts it on the wire:
// its name on that surface, its schema keywords, its default. It is deliberately
// separate from Arg — Arg is the contract-level description shared by the SDKs,
// SurfaceArg is what a client actually sends, and the two legitimately differ
// (MCP calls eval's argument "expression", JSONL calls it "expr").
type SurfaceArg struct {
	Name        string
	Type        ArgType
	Required    bool
	Description string
	// Enum restricts the accepted values (JSON Schema "enum").
	Enum []string
	// Items is the element type when Type is ArgArray.
	Items ArgType
	// Default is the schema default: string, bool, int or float64.
	// nil means the surface declares no default.
	Default any
}

// SurfaceSpec is an op's declaration on one surface: the description a client
// or a model reads, and the arguments in declaration order (the order also
// fixes the JSON Schema "required" array).
type SurfaceSpec struct {
	Description string
	Args        []SurfaceArg
	// Order fixes the position of the tool in the list handed to an LLM, where
	// order carries a weak recall bias. Only the AI surface uses it; MCP sorts
	// its tools by name before answering tools/list. Ops sharing an Order fall
	// back to alphabetical.
	Order int
}

// Op is one entry in the canonical operation catalog.
type Op struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Args    []Arg  `json:"args"`
	// Surfaces lists which protocol surfaces expose this op.
	// Known values: "jsonl", "mcp", "ai".
	Surfaces []string `json:"surfaces"`

	// MCP and AI carry the wire declaration for those two surfaces. They are
	// excluded from contracts/commands.json on purpose: the contract the SDKs
	// are typed against is frozen, and it describes ops, not per-surface
	// schemas. The generator refuses to run if the Surfaces list and the
	// presence of these specs disagree.
	MCP *SurfaceSpec `json:"-"`
	AI  *SurfaceSpec `json:"-"`
}

// Catalog returns the full canonical list of ghostchrome agent operations,
// sorted alphabetically by name.
//
// Surface coverage notes (divergences are intentional, not bugs):
//
//   - "snapshot" is MCP-only: it bundles navigate+extract+errors into one call
//     to amortize the per-tool token cost in MCP's tools/list. JSONL has
//     dedicated ops for each concern.
//   - "done" is AI-only: it is a meta-tool that signals goal completion to
//     the Anthropic tool-use loop. It has no CDP side-effect and no JSONL/MCP
//     equivalent.
//   - "wait_for" is MCP-only: it extends the basic "wait" op with text-match
//     and a timeout_ms parameter. The JSONL surface uses "wait" with selector/ms.
//   - "fill" and "init" and "close" are JSONL-only: fill is a convenience
//     multi-field wrapper; init/close manage session lifecycle that MCP/AI
//     handle implicitly.
//   - "extract" and "hover" and "scroll_by"/"scroll_to" appear in JSONL and AI
//     but not MCP (snapshot replaces extract in MCP; hover/scroll are available
//     via eval in MCP).
//   - "errors" and "url" appear in JSONL and AI but not MCP (covered by snapshot).
//   - "screenshot" appears in JSONL and MCP but not AI.
//   - "emulate" and "swipe" are MCP-only: the CLI covers them with the
//     `emulate` command plus a session-persisted profile, and the JSONL loop
//     runs in one process where `ghostchrome batch` can carry an emulate verb.
//     MCP holds a long-lived browser, so it needs its own device switch and a
//     touch-event gesture (drag is mouse-only and a mobile PWA ignores it).
//
// The MCP and AI specs below are the texts those surfaces published before the
// catalog became executable, kept verbatim: they are read by MCP clients and by
// models, so generation reproduces them rather than rewriting them from Summary.
func Catalog() []Op {
	return []Op{
		{
			Name:     "back",
			Summary:  "Navigate back in browser history.",
			Args:     []Arg{},
			Surfaces: []string{"jsonl", "mcp"},
			MCP: &SurfaceSpec{
				Description: "Navigate back in browser history. Returns the new URL.",
			},
		},
		{
			Name:    "check",
			Summary: "Tick a checkbox or radio by @ref (idempotent — no-op if already checked).",
			Args: []Arg{
				{Name: "ref", Type: ArgString, Required: true, Description: "@ref of the checkbox/radio"},
				{Name: "snapshot", Type: ArgString, Required: false, Description: "none | diff | full (default: diff)"},
			},
			Surfaces: []string{"jsonl"},
		},
		{
			Name:    "click",
			Summary: "Click an element by its @ref from the last extract/snapshot.",
			Args: []Arg{
				{Name: "ref", Type: ArgString, Required: true, Description: "@ref of the element, e.g. @5"},
				{Name: "button", Type: ArgString, Required: false, Description: "Mouse button: left, right, or middle (default: left)"},
				{Name: "snapshot", Type: ArgString, Required: false, Description: "none | diff | full (default: diff)"},
			},
			Surfaces: []string{"jsonl", "mcp", "ai"},
			MCP: &SurfaceSpec{
				Description: "Click an element by its ref (@1, @2, ...). Ref comes from the last snapshot. Auto-waits for attached + visible + enabled + hit-target, then returns a compact a11y-ref diff (snapshot=diff, default), a skeleton extract (full), or nothing (none).",
				Args: []SurfaceArg{
					{Name: "ref", Type: ArgString, Required: true, Description: "Element ref from the last snapshot (e.g. @3 or 3)"},
					{Name: "button", Type: ArgString, Description: "Mouse button: left, right, or middle (default: left)", Enum: []string{"left", "right", "middle"}, Default: "left"},
					{Name: "snapshot", Type: ArgString, Description: "none | diff | full (default: diff)", Enum: []string{"none", "diff", "full"}, Default: "diff"},
				},
			},
			AI: &SurfaceSpec{
				Order:       3,
				Description: "Click an element by its @ref (from extract). Refs become stale after navigation — re-extract first.",
				Args: []SurfaceArg{
					{Name: "ref", Type: ArgString, Required: true, Description: "@ref of the element, e.g. @5"},
				},
			},
		},
		{
			Name:     "close",
			Summary:  "Close the browser session and exit the agent loop.",
			Args:     []Arg{},
			Surfaces: []string{"jsonl"},
		},
		{
			Name:    "dblclick",
			Summary: "Double-click an element by its @ref from the last extract/snapshot.",
			Args: []Arg{
				{Name: "ref", Type: ArgString, Required: true, Description: "@ref of the element, e.g. @5"},
				{Name: "button", Type: ArgString, Required: false, Description: "Mouse button: left, right, or middle (default: left)"},
				{Name: "snapshot", Type: ArgString, Required: false, Description: "none | diff | full (default: diff)"},
			},
			Surfaces: []string{"jsonl"},
		},
		{
			Name:    "dialog",
			Summary: "Set how JavaScript dialogs (alert/confirm/prompt) are auto-handled. Default accept.",
			Args: []Arg{
				{Name: "action", Type: ArgString, Required: false, Description: "accept (default) | dismiss"},
				{Name: "text", Type: ArgString, Required: false, Description: "Prompt response text when action=accept"},
			},
			Surfaces: []string{"jsonl", "mcp"},
			MCP: &SurfaceSpec{
				Description: "Set how JavaScript dialogs (alert/confirm/prompt) are handled. Default is accept. Applies to subsequent dialogs.",
				Args: []SurfaceArg{
					{Name: "action", Type: ArgString, Description: "accept (default) or dismiss", Enum: []string{"accept", "dismiss"}, Default: "accept"},
					{Name: "text", Type: ArgString, Description: "Prompt response text when action=accept"},
				},
			},
		},
		{
			Name:    "done",
			Summary: "Signal that the goal is complete (AI surface only — no CDP side-effect).",
			Args: []Arg{
				{Name: "answer", Type: ArgString, Required: true, Description: "Final answer or short summary of the outcome"},
			},
			Surfaces: []string{"ai"},
			AI: &SurfaceSpec{
				Order:       14,
				Description: "Signal that the goal is complete. Provide the answer / outcome.",
				Args: []SurfaceArg{
					{Name: "answer", Type: ArgString, Required: true, Description: "Final answer or short summary of the outcome"},
				},
			},
		},
		{
			Name:    "drag",
			Summary: "Drag an element from one @ref to another (full mouse drag-and-drop). MCP surface only.",
			Args: []Arg{
				{Name: "from", Type: ArgString, Required: true, Description: "Source element @ref"},
				{Name: "to", Type: ArgString, Required: true, Description: "Target element @ref"},
				{Name: "steps", Type: ArgNumber, Required: false, Description: "Intermediate mouse move steps (default 10)"},
			},
			Surfaces: []string{"mcp"},
			MCP: &SurfaceSpec{
				Description: "Drag an element from one ref to another. Simulates a full mouse drag-and-drop sequence.",
				Args: []SurfaceArg{
					{Name: "from", Type: ArgString, Required: true, Description: "Source element ref"},
					{Name: "to", Type: ArgString, Required: true, Description: "Target element ref"},
					{Name: "steps", Type: ArgNumber, Description: "Intermediate mouse move steps (default 10)", Default: 10},
				},
			},
		},
		{
			Name:    "emulate",
			Summary: "Emulate a device: viewport, DPR, mobile flag, touch, user-agent, color-scheme, safe-area insets. MCP surface only (the CLI has `ghostchrome emulate`).",
			Args: []Arg{
				{Name: "device", Type: ArgString, Required: false, Description: "Preset name, e.g. iphone-14-pro-max, pixel-7, ipad, desktop"},
				{Name: "width", Type: ArgNumber, Required: false, Description: "Viewport width in CSS pixels"},
				{Name: "height", Type: ArgNumber, Required: false, Description: "Viewport height in CSS pixels"},
				{Name: "device_scale_factor", Type: ArgNumber, Required: false, Description: "devicePixelRatio (default 1 or the preset's)"},
				{Name: "mobile", Type: ArgBoolean, Required: false, Description: "Mobile viewport semantics (meta viewport, screen size)"},
				{Name: "touch", Type: ArgBoolean, Required: false, Description: "Touch input emulation (pointer:coarse, maxTouchPoints)"},
				{Name: "user_agent", Type: ArgString, Required: false, Description: "Override navigator.userAgent and the User-Agent header"},
				{Name: "color_scheme", Type: ArgString, Required: false, Description: "prefers-color-scheme: dark | light | no-preference"},
				{Name: "safe_area_top", Type: ArgNumber, Required: false, Description: "env(safe-area-inset-top) in CSS pixels"},
				{Name: "safe_area_right", Type: ArgNumber, Required: false, Description: "env(safe-area-inset-right) in CSS pixels"},
				{Name: "safe_area_bottom", Type: ArgNumber, Required: false, Description: "env(safe-area-inset-bottom) in CSS pixels"},
				{Name: "safe_area_left", Type: ArgNumber, Required: false, Description: "env(safe-area-inset-left) in CSS pixels"},
				{Name: "reset", Type: ArgBoolean, Required: false, Description: "Drop every emulation override and restore the desktop viewport/UA"},
			},
			Surfaces: []string{"mcp"},
			MCP: &SurfaceSpec{
				Description: "Emulate a device: viewport size, devicePixelRatio, mobile flag, touch input, user-agent, prefers-color-scheme, and safe-area insets. Use it before testing a responsive or mobile-only UI — without it the page is a 1920x1080 desktop with pointer:fine, so a phone shell or a coarse-pointer media query never activates. Pass a preset via `device`, or explicit width/height. For a standalone PWA on a notched iPhone also pass safe_area_top=59 and safe_area_bottom=34: without them env(safe-area-inset-*) is 0 and every header or bar that pads the status bar or home indicator is measured wrong. Pass reset=true to go back to a plain desktop tab. Invalidates refs: re-snapshot afterwards.",
				Args: []SurfaceArg{
					{Name: "device", Type: ArgString, Description: "Preset: iphone-se, iphone-14, iphone-14-pro, iphone-14-pro-max, pixel-7, pixel-8-pro, ipad, ipad-pro, desktop, desktop-2k"},
					{Name: "width", Type: ArgNumber, Description: "Viewport width in CSS pixels (overrides the preset)"},
					{Name: "height", Type: ArgNumber, Description: "Viewport height in CSS pixels (overrides the preset)"},
					{Name: "device_scale_factor", Type: ArgNumber, Description: "devicePixelRatio, e.g. 3 for a modern iPhone (default 1, or the preset's)"},
					{Name: "mobile", Type: ArgBoolean, Description: "Mobile viewport semantics: meta viewport, mobile scrollbars, screen size"},
					{Name: "touch", Type: ArgBoolean, Description: "Touch input emulation: pointer:coarse, navigator.maxTouchPoints > 0, touch events"},
					{Name: "user_agent", Type: ArgString, Description: "Override navigator.userAgent and the User-Agent header"},
					{Name: "color_scheme", Type: ArgString, Description: "Emulate prefers-color-scheme", Enum: []string{"dark", "light", "no-preference"}},
					{Name: "safe_area_top", Type: ArgNumber, Description: "env(safe-area-inset-top) in CSS pixels (59 on an iPhone 15 Pro Max in standalone mode)"},
					{Name: "safe_area_right", Type: ArgNumber, Description: "env(safe-area-inset-right) in CSS pixels"},
					{Name: "safe_area_bottom", Type: ArgNumber, Description: "env(safe-area-inset-bottom) in CSS pixels (34 for the iPhone home indicator)"},
					{Name: "safe_area_left", Type: ArgNumber, Description: "env(safe-area-inset-left) in CSS pixels"},
					{Name: "reset", Type: ArgBoolean, Description: "Drop every emulation override and restore the real desktop viewport and UA", Default: false},
				},
			},
		},
		{
			Name:     "errors",
			Summary:  "Return console and network errors observed on the current page.",
			Args:     []Arg{},
			Surfaces: []string{"jsonl", "ai"},
			AI: &SurfaceSpec{
				Order:       12,
				Description: "Return console + network errors observed on the current page.",
			},
		},
		{
			Name:    "eval",
			Summary: "Evaluate a JavaScript expression on the page and return the stringified result.",
			Args: []Arg{
				{Name: "expr", Type: ArgString, Required: true, Description: "JS expression (JSONL/AI key); MCP uses 'expression'"},
				{Name: "ref", Type: ArgString, Required: false, Description: "Optional @ref to bind as `this`"},
				// MCP-only extra arg documented here for completeness:
				{Name: "timeout_ms", Type: ArgNumber, Required: false, Description: "Per-call deadline in ms (MCP surface only, default 8000)"},
			},
			Surfaces: []string{"jsonl", "mcp", "ai"},
			MCP: &SurfaceSpec{
				Description: "Evaluate a JavaScript expression in the page and return its serialized value. Async expressions are awaited. Use this as an escape hatch for anything the other tools can't do (read localStorage, dispatch a custom event, scroll, etc.).",
				Args: []SurfaceArg{
					{Name: "expression", Type: ArgString, Required: true, Description: "JS expression. Top-level await OK."},
					{Name: "ref", Type: ArgString, Description: "Optional @ref to scope `this` to an element"},
					{Name: "timeout_ms", Type: ArgNumber, Description: "Per-call deadline in milliseconds (default 8000)", Default: 8000},
				},
			},
			AI: &SurfaceSpec{
				Order:       10,
				Description: "Evaluate a JavaScript expression on the page. Returns the stringified value. Use sparingly — prefer extract+click.",
				Args: []SurfaceArg{
					{Name: "expr", Type: ArgString, Required: true, Description: "JS expression"},
					{Name: "ref", Type: ArgString, Description: "Optional @ref bound as `this`"},
				},
			},
		},
		{
			Name:    "extract",
			Summary: "Return a compact accessibility tree of the current page with @refs.",
			Args: []Arg{
				{Name: "level", Type: ArgString, Required: false, Description: "skeleton | content | full (default: content)"},
				{Name: "selector", Type: ArgString, Required: false, Description: "Optional CSS selector to scope extraction"},
			},
			Surfaces: []string{"jsonl", "ai"},
			AI: &SurfaceSpec{
				Order:       2,
				Description: "Return a compact accessibility tree of the current page with @refs you can click/type on. Always call this before clicking or typing on an unknown page.",
				Args: []SurfaceArg{
					{Name: "level", Type: ArgString, Description: "Default content. Use skeleton for huge pages.", Enum: []string{"skeleton", "content", "full"}},
					{Name: "selector", Type: ArgString, Description: "Optional CSS selector to scope the extraction"},
				},
			},
		},
		{
			Name:    "fill",
			Summary: "Fill multiple form fields in one call (JSONL convenience wrapper over type).",
			Args: []Arg{
				{Name: "fields", Type: ArgObject, Required: true, Description: "Map of @ref → value strings"},
			},
			Surfaces: []string{"jsonl"},
		},
		{
			Name:    "fill_form",
			Summary: "Fill multiple form fields in one call (MCP surface name for the JSONL 'fill' op).",
			Args: []Arg{
				{Name: "fields", Type: ArgString, Required: true, Description: "JSON object mapping @ref to text value"},
			},
			Surfaces: []string{"mcp"},
			MCP: &SurfaceSpec{
				Description: "Fill multiple form fields in one call. Pass a JSON object mapping refs to values: {\"@1\": \"John\", \"@2\": \"john@example.com\"}",
				Args: []SurfaceArg{
					{Name: "fields", Type: ArgString, Required: true, Description: "JSON object mapping @ref to text value"},
				},
			},
		},
		{
			Name:     "forward",
			Summary:  "Navigate forward in browser history.",
			Args:     []Arg{},
			Surfaces: []string{"jsonl", "mcp"},
			MCP: &SurfaceSpec{
				Description: "Navigate forward in browser history. Returns the new URL.",
			},
		},
		{
			Name:    "hover",
			Summary: "Hover over an element by @ref (reveals dropdowns, tooltips).",
			Args: []Arg{
				{Name: "ref", Type: ArgString, Required: true, Description: "@ref of the element"},
				{Name: "snapshot", Type: ArgString, Required: false, Description: "none | diff | full (default: diff)"},
			},
			Surfaces: []string{"jsonl", "mcp", "ai"},
			MCP: &SurfaceSpec{
				Description: "Hover over an element by ref. Returns a compact a11y-ref diff (snapshot=diff, default), a skeleton extract (full), or nothing (none).",
				Args: []SurfaceArg{
					{Name: "ref", Type: ArgString, Required: true, Description: "Element ref from the last snapshot"},
					{Name: "snapshot", Type: ArgString, Description: "none | diff | full (default: diff)", Enum: []string{"none", "diff", "full"}, Default: "diff"},
				},
			},
			AI: &SurfaceSpec{
				Order:       6,
				Description: "Hover over an element by @ref (reveals dropdowns, tooltips).",
				Args: []SurfaceArg{
					{Name: "ref", Type: ArgString, Required: true, Description: "@ref"},
				},
			},
		},
		{
			Name:     "init",
			Summary:  "Open the browser (no-op if already open). JSONL session lifecycle op.",
			Args:     []Arg{},
			Surfaces: []string{"jsonl"},
		},
		{
			Name:    "navigate",
			Summary: "Load a URL in the current tab.",
			Args: []Arg{
				{Name: "url", Type: ArgString, Required: true, Description: "Absolute URL"},
				{Name: "wait", Type: ArgString, Required: false, Description: "load | stable | idle | none | domcontentloaded (default: load for JSONL/AI, domcontentloaded for MCP)"},
			},
			Surfaces: []string{"jsonl", "mcp", "ai"},
			MCP: &SurfaceSpec{
				Description: "Navigate to a URL without a full snapshot. Returns only status + title + load time. Use when you don't need the DOM yet (e.g. chaining click-through pages).",
				Args: []SurfaceArg{
					{Name: "url", Type: ArgString, Required: true, Description: "Absolute URL to navigate to (https://...)"},
					{Name: "wait", Type: ArgString, Description: "domcontentloaded (default), load, stable, idle, none", Enum: []string{"domcontentloaded", "load", "stable", "idle", "none"}, Default: "domcontentloaded"},
				},
			},
			AI: &SurfaceSpec{
				Order:       1,
				Description: "Load a URL in the current tab. Use this first if the goal references a site.",
				Args: []SurfaceArg{
					{Name: "url", Type: ArgString, Required: true, Description: "Absolute URL"},
					{Name: "wait", Type: ArgString, Enum: []string{"load", "stable", "idle", "none"}},
				},
			},
		},
		{
			Name:    "press",
			Summary: "Press a keyboard key; optionally focus an element by @ref first.",
			Args: []Arg{
				{Name: "key", Type: ArgString, Required: true, Description: "Key name, e.g. Enter, Escape, ArrowDown"},
				{Name: "ref", Type: ArgString, Required: false, Description: "Optional @ref to focus before pressing"},
				{Name: "snapshot", Type: ArgString, Required: false, Description: "none | diff | full (default: diff)"},
			},
			Surfaces: []string{"jsonl", "mcp", "ai"},
			MCP: &SurfaceSpec{
				Description: "Press a keyboard key. If `ref` is provided, focus that element first; otherwise the key fires on the document.",
				Args: []SurfaceArg{
					{Name: "key", Type: ArgString, Required: true, Description: "Key name: Enter, Tab, Escape, ArrowDown, ArrowUp, PageDown, Backspace, ..."},
					{Name: "ref", Type: ArgString, Description: "Optional element ref to focus before pressing"},
				},
			},
			AI: &SurfaceSpec{
				Order:       5,
				Description: "Press a keyboard key (e.g. Enter, Escape, ArrowDown). Optional ref to focus first.",
				Args: []SurfaceArg{
					{Name: "key", Type: ArgString, Required: true, Description: "Key name"},
					{Name: "ref", Type: ArgString, Description: "Optional @ref to focus"},
				},
			},
		},
		{
			Name:     "reload",
			Summary:  "Reload (refresh) the current page.",
			Args:     []Arg{},
			Surfaces: []string{"jsonl"},
		},
		{
			Name:    "screenshot",
			Summary: "Capture the current viewport (or element) as a PNG/JPEG/WebP image.",
			Args: []Arg{
				{Name: "full_page", Type: ArgBoolean, Required: false, Description: "Capture full scrollable page (default: false)"},
				{Name: "ref", Type: ArgString, Required: false, Description: "Capture only this element by @ref"},
				{Name: "quality", Type: ArgInteger, Required: false, Description: "JPEG quality 1-100 (JSONL/AI); also used for WebP in MCP"},
				// MCP-only:
				{Name: "format", Type: ArgString, Required: false, Description: "webp | jpeg | png (MCP surface only, default: webp)"},
			},
			Surfaces: []string{"jsonl", "mcp"},
			MCP: &SurfaceSpec{
				Description: "Capture the current page (or one element by ref) as an image embedded in the MCP result. Defaults to WebP quality 60 — typically 30-70% lighter than JPEG/PNG for UI captures. Use annotate=true to overlay numbered borders on interactive elements.",
				Args: []SurfaceArg{
					{Name: "ref", Type: ArgString, Description: "Capture only this element by @ref"},
					{Name: "full_page", Type: ArgBoolean, Description: "Capture the full scrollable page instead of the viewport", Default: false},
					{Name: "format", Type: ArgString, Description: "Image format: webp (default), jpeg, png", Enum: []string{"webp", "jpeg", "png"}, Default: "webp"},
					{Name: "quality", Type: ArgNumber, Description: "Quality 1-100 for webp/jpeg (default 60). Ignored for png.", Default: 60},
					{Name: "annotate", Type: ArgBoolean, Description: "Overlay numbered borders on interactive elements (forces PNG output)", Default: false},
				},
			},
		},
		{
			Name:    "scroll_by",
			Summary: "Scroll the viewport vertically by dy pixels (positive = down).",
			Args: []Arg{
				{Name: "dy", Type: ArgInteger, Required: true, Description: "Pixels to scroll (signed)"},
			},
			Surfaces: []string{"jsonl", "ai"},
			AI: &SurfaceSpec{
				Order:       8,
				Description: "Scroll the viewport vertically by dy pixels (positive=down).",
				Args: []SurfaceArg{
					{Name: "dy", Type: ArgInteger, Required: true, Description: "Pixels (signed)"},
				},
			},
		},
		{
			Name:    "scroll_to",
			Summary: "Scroll to an absolute Y position or to the bottom of the page.",
			Args: []Arg{
				{Name: "y", Type: ArgInteger, Required: false, Description: "Absolute Y coordinate"},
				{Name: "bottom", Type: ArgBoolean, Required: false, Description: "If true, scroll to the page bottom"},
			},
			Surfaces: []string{"jsonl", "ai"},
			AI: &SurfaceSpec{
				Order:       9,
				Description: "Scroll to absolute Y or to bottom.",
				Args: []SurfaceArg{
					{Name: "y", Type: ArgInteger, Description: "Absolute Y"},
					{Name: "bottom", Type: ArgBoolean},
				},
			},
		},
		{
			Name:    "select",
			Summary: "Pick one or more options in a <select> element by @ref.",
			Args: []Arg{
				{Name: "ref", Type: ArgString, Required: true, Description: "@ref of the <select> element"},
				// JSONL/AI use "values" (array); MCP uses "value" (string, JSON-array-as-string for multi-select).
				{Name: "values", Type: ArgArray, Required: true, Description: "Option values to select (JSONL/AI: string array; MCP: 'value' string or JSON-array string)"},
				{Name: "snapshot", Type: ArgString, Required: false, Description: "none | diff | full (default: diff)"},
			},
			Surfaces: []string{"jsonl", "mcp", "ai"},
			MCP: &SurfaceSpec{
				Description: "Select one or more options in a <select> element by ref. Returns a compact a11y-ref diff (snapshot=diff, default), a skeleton extract (full), or nothing (none).",
				Args: []SurfaceArg{
					{Name: "ref", Type: ArgString, Required: true, Description: "Element ref of the <select>"},
					{Name: "value", Type: ArgString, Required: true, Description: "Option value or visible label to select. For multi-select, pass JSON array as string (e.g. \"[\\\"a\\\",\\\"b\\\"]\")."},
					{Name: "snapshot", Type: ArgString, Description: "none | diff | full (default: diff)", Enum: []string{"none", "diff", "full"}, Default: "diff"},
				},
			},
			AI: &SurfaceSpec{
				Order:       7,
				Description: "Pick option(s) in a <select> by @ref.",
				Args: []SurfaceArg{
					{Name: "ref", Type: ArgString, Required: true, Description: "@ref of <select>"},
					{Name: "values", Type: ArgArray, Required: true, Items: ArgString},
				},
			},
		},
		{
			Name:    "snapshot",
			Summary: "All-in-one page report: navigate (optional) + errors + DOM extract. MCP surface only.",
			Args: []Arg{
				{Name: "url", Type: ArgString, Required: false, Description: "URL to navigate to before snapshotting (optional)"},
				{Name: "wait", Type: ArgString, Required: false, Description: "Wait strategy: domcontentloaded | load | stable | idle | none"},
				{Name: "level", Type: ArgString, Required: false, Description: "skeleton | content | full (default: content)"},
				{Name: "selector", Type: ArgString, Required: false, Description: "Optional CSS selector to scope DOM extraction"},
			},
			Surfaces: []string{"mcp"},
			MCP: &SurfaceSpec{
				Description: "All-in-one page report: status, console+network errors, compact DOM with refs (@1, @2, ...). If `url` is given, navigate first; otherwise snapshot the current page. This is the canonical first call when an agent visits or revisits a page. Refs from this snapshot stay valid until the next snapshot or navigate.",
				Args: []SurfaceArg{
					{Name: "url", Type: ArgString, Description: "Absolute URL to navigate to before snapshotting (optional — omit to snapshot the current page)"},
					{Name: "wait", Type: ArgString, Description: "Wait strategy when navigating: domcontentloaded (default), load, stable, idle, none", Enum: []string{"domcontentloaded", "load", "stable", "idle", "none"}, Default: "domcontentloaded"},
					{Name: "level", Type: ArgString, Description: "DOM extraction depth: skeleton (interactive only, smallest), content (default — adds text), full (everything named)", Enum: []string{"skeleton", "content", "full"}, Default: "content"},
					{Name: "selector", Type: ArgString, Description: "Optional CSS selector to scope the DOM extraction to a subtree"},
				},
			},
		},
		{
			Name:    "swipe",
			Summary: "Swipe with a real single-finger touch gesture between two viewport coordinates. MCP surface only.",
			Args: []Arg{
				{Name: "from_x", Type: ArgNumber, Required: true, Description: "Start X in CSS pixels, relative to the viewport"},
				{Name: "from_y", Type: ArgNumber, Required: true, Description: "Start Y in CSS pixels, relative to the viewport"},
				{Name: "to_x", Type: ArgNumber, Required: true, Description: "End X in CSS pixels"},
				{Name: "to_y", Type: ArgNumber, Required: true, Description: "End Y in CSS pixels"},
				{Name: "duration_ms", Type: ArgNumber, Required: false, Description: "Gesture duration in ms (default 300, max 10000)"},
				{Name: "steps", Type: ArgNumber, Required: false, Description: "Intermediate touchmove events (default 12, max 100)"},
				{Name: "snapshot", Type: ArgString, Required: false, Description: "none | diff | full (default: diff)"},
			},
			Surfaces: []string{"mcp"},
			MCP: &SurfaceSpec{
				Description: "Swipe with a real single-finger touch gesture (touchstart/touchmove/touchend) between two viewport coordinates in CSS pixels. Use this — not drag — to test a mobile drawer, carousel, or pull-to-refresh: drag emits mouse events, which a touch-only handler ignores. Enables touch emulation automatically if it is off.",
				Args: []SurfaceArg{
					{Name: "from_x", Type: ArgNumber, Required: true, Description: "Start X in CSS pixels, relative to the viewport"},
					{Name: "from_y", Type: ArgNumber, Required: true, Description: "Start Y in CSS pixels, relative to the viewport"},
					{Name: "to_x", Type: ArgNumber, Required: true, Description: "End X in CSS pixels"},
					{Name: "to_y", Type: ArgNumber, Required: true, Description: "End Y in CSS pixels"},
					{Name: "duration_ms", Type: ArgNumber, Description: "Gesture duration in milliseconds (default 300). Shorter = flick, longer = slow drag.", Default: 300},
					{Name: "steps", Type: ArgNumber, Description: "Intermediate touchmove events (default 12)", Default: 12},
					{Name: "snapshot", Type: ArgString, Description: "none | diff | full (default: diff)", Enum: []string{"none", "diff", "full"}, Default: "diff"},
				},
			},
		},
		{
			Name:    "tabs",
			Summary: "List, switch, close, or open browser tabs.",
			Args: []Arg{
				{Name: "action", Type: ArgString, Required: false, Description: "list (default) | switch | close | new"},
				{Name: "index", Type: ArgNumber, Required: false, Description: "Tab index for switch/close actions"},
				{Name: "url", Type: ArgString, Required: false, Description: "URL for action=new (blank tab when omitted)"},
			},
			Surfaces: []string{"jsonl", "mcp"},
			MCP: &SurfaceSpec{
				Description: "List open browser tabs with their URLs, titles, and indices. Use the index with 'switch' action to change the active tab.",
				Args: []SurfaceArg{
					{Name: "action", Type: ArgString, Description: "list (default), switch, close, new", Enum: []string{"list", "switch", "close", "new"}, Default: "list"},
					{Name: "index", Type: ArgNumber, Description: "Tab index for switch/close actions"},
					{Name: "url", Type: ArgString, Description: "URL for action=new (blank tab when omitted)"},
				},
			},
		},
		{
			Name:    "type",
			Summary: "Type text into an input/textarea identified by @ref.",
			Args: []Arg{
				{Name: "ref", Type: ArgString, Required: true, Description: "@ref of the input element"},
				{Name: "text", Type: ArgString, Required: true, Description: "Text to type (field is cleared first)"},
				{Name: "submit", Type: ArgBoolean, Required: false, Description: "If true, press Enter after typing (submit the form)"},
				{Name: "snapshot", Type: ArgString, Required: false, Description: "none | diff | full (default: diff)"},
			},
			Surfaces: []string{"jsonl", "mcp", "ai"},
			MCP: &SurfaceSpec{
				Description: "Type text into an input/textarea by ref. Set submit=true to press Enter after typing. Returns a compact a11y-ref diff (snapshot=diff, default), a skeleton extract (full), or nothing (none).",
				Args: []SurfaceArg{
					{Name: "ref", Type: ArgString, Required: true, Description: "Element ref from the last snapshot"},
					{Name: "text", Type: ArgString, Required: true, Description: "Text to type (the field is cleared first)"},
					{Name: "submit", Type: ArgBoolean, Description: "If true, press Enter after typing", Default: false},
					{Name: "snapshot", Type: ArgString, Description: "none | diff | full (default: diff)", Enum: []string{"none", "diff", "full"}, Default: "diff"},
				},
			},
			AI: &SurfaceSpec{
				Order:       4,
				Description: "Type text into an input identified by @ref.",
				Args: []SurfaceArg{
					{Name: "ref", Type: ArgString, Required: true, Description: "@ref of the input"},
					{Name: "text", Type: ArgString, Required: true, Description: "Text to type"},
				},
			},
		},
		{
			Name:    "uncheck",
			Summary: "Untick a checkbox by @ref (idempotent — no-op if already unchecked).",
			Args: []Arg{
				{Name: "ref", Type: ArgString, Required: true, Description: "@ref of the checkbox"},
				{Name: "snapshot", Type: ArgString, Required: false, Description: "none | diff | full (default: diff)"},
			},
			Surfaces: []string{"jsonl"},
		},
		{
			Name:    "upload",
			Summary: "Upload file(s) to a file <input> element by @ref. MCP surface only.",
			Args: []Arg{
				{Name: "ref", Type: ArgString, Required: true, Description: "File input element @ref"},
				{Name: "paths", Type: ArgString, Required: true, Description: "File path or JSON array of file paths"},
			},
			Surfaces: []string{"mcp"},
			MCP: &SurfaceSpec{
				Description: "Upload file(s) to a file input element by ref.",
				Args: []SurfaceArg{
					{Name: "ref", Type: ArgString, Required: true, Description: "File input element ref"},
					{Name: "paths", Type: ArgString, Required: true, Description: "File path or JSON array of file paths"},
				},
			},
		},
		{
			Name:     "url",
			Summary:  "Return the current page URL and title.",
			Args:     []Arg{},
			Surfaces: []string{"jsonl", "ai"},
			AI: &SurfaceSpec{
				Order:       13,
				Description: "Return the current page URL and title.",
			},
		},
		{
			Name:    "wait",
			Summary: "Wait for a selector/@ref, visible text, URL substring, load state, or a fixed delay.",
			Args: []Arg{
				{Name: "selector", Type: ArgString, Required: false, Description: "CSS selector to wait for"},
				{Name: "ref", Type: ArgString, Required: false, Description: "@ref from the last snapshot to wait for"},
				{Name: "text", Type: ArgString, Required: false, Description: "Visible text substring to wait for"},
				{Name: "url", Type: ArgString, Required: false, Description: "Wait until the current URL contains this substring"},
				{Name: "load", Type: ArgString, Required: false, Description: "Page load state: load | domcontentloaded | idle | stable | none"},
				{Name: "state", Type: ArgString, Required: false, Description: "Element state: attached | visible | hidden | enabled | stable (default: visible)"},
				{Name: "ms", Type: ArgInteger, Required: false, Description: "Fixed delay in milliseconds"},
				{Name: "timeout_ms", Type: ArgInteger, Required: false, Description: "Maximum wait time in milliseconds"},
			},
			Surfaces: []string{"jsonl", "ai"},
			AI: &SurfaceSpec{
				Order:       11,
				Description: "Wait for a CSS selector to appear or a fixed delay.",
				Args: []SurfaceArg{
					{Name: "selector", Type: ArgString, Description: "CSS selector"},
					{Name: "ms", Type: ArgInteger, Description: "Fixed delay in ms"},
				},
			},
		},
		{
			Name:    "wait_for",
			Summary: "Wait for a @ref/selector/text/url/load condition. MCP surface only (JSONL uses 'wait').",
			Args: []Arg{
				{Name: "ref", Type: ArgString, Required: false, Description: "@ref from the last snapshot to wait for"},
				{Name: "selector", Type: ArgString, Required: false, Description: "CSS selector to wait for"},
				{Name: "text", Type: ArgString, Required: false, Description: "Visible text substring to wait for"},
				{Name: "url", Type: ArgString, Required: false, Description: "Wait until the current URL contains this substring"},
				{Name: "load", Type: ArgString, Required: false, Description: "Page load state: load | domcontentloaded | idle | stable | none"},
				{Name: "state", Type: ArgString, Required: false, Description: "Element state: attached | visible | hidden | enabled | stable (default: visible)"},
				{Name: "timeout_ms", Type: ArgNumber, Required: false, Description: "Maximum wait time in ms (default 5000, max 30000)"},
			},
			Surfaces: []string{"mcp"},
			MCP: &SurfaceSpec{
				Description: "Wait for a condition: @ref or CSS selector to become visible, text to appear, or just a timeout. At least one of ref/selector/text/timeout_ms must be given.",
				Args: []SurfaceArg{
					{Name: "ref", Type: ArgString, Description: "Element ref from the last snapshot (e.g. @3)"},
					{Name: "selector", Type: ArgString, Description: "CSS selector to wait for"},
					{Name: "text", Type: ArgString, Description: "Visible text substring to wait for"},
					{Name: "url", Type: ArgString, Description: "Wait until the current URL contains this substring"},
					{Name: "load", Type: ArgString, Description: "Page load state: load, domcontentloaded, idle, stable, none", Enum: []string{"load", "domcontentloaded", "idle", "stable", "none"}},
					{Name: "state", Type: ArgString, Description: "Element state when waiting on ref/selector: attached, visible, hidden, enabled, stable (default: visible)", Enum: []string{"attached", "visible", "hidden", "enabled", "stable"}, Default: "visible"},
					{Name: "timeout_ms", Type: ArgNumber, Description: "Maximum wait time in milliseconds (default 5000, max 30000)", Default: 5000},
				},
			},
		},
	}
}
