// Package ops defines the canonical catalog of ghostchrome agent operations.
//
// This is the single source of truth for op names, summaries, argument shapes,
// and which surfaces expose each op. The three live surfaces are:
//
//   - "jsonl" — internal/runtime dispatch table, framed by cmd/agent.go
//   - "mcp"   — internal/surface/mcp/tools.go (registerTools)
//   - "ai"    — internal/surface/ai/tools.go (ToolSpecs)
//
// IMPORTANT: this file is additive. The three surface files are NOT modified
// to consume this registry — that refactor is a separate, future task.
// The registry is used today for:
//   - generating contracts/commands.json (go:generate)
//   - the parity test (internal/ops/parity_test.go)
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

// Op is one entry in the canonical operation catalog.
type Op struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Args    []Arg  `json:"args"`
	// Surfaces lists which protocol surfaces expose this op.
	// Known values: "jsonl", "mcp", "ai".
	Surfaces []string `json:"surfaces"`
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
func Catalog() []Op {
	return []Op{
		{
			Name:     "back",
			Summary:  "Navigate back in browser history.",
			Args:     []Arg{},
			Surfaces: []string{"jsonl", "mcp"},
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
		},
		{
			Name:    "done",
			Summary: "Signal that the goal is complete (AI surface only — no CDP side-effect).",
			Args: []Arg{
				{Name: "answer", Type: ArgString, Required: true, Description: "Final answer or short summary of the outcome"},
			},
			Surfaces: []string{"ai"},
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
		},
		{
			Name:    "emulate",
			Summary: "Emulate a device: viewport, DPR, mobile flag, touch, user-agent, color-scheme. MCP surface only (the CLI has `ghostchrome emulate`).",
			Args: []Arg{
				{Name: "device", Type: ArgString, Required: false, Description: "Preset name, e.g. iphone-14-pro-max, pixel-7, ipad, desktop"},
				{Name: "width", Type: ArgNumber, Required: false, Description: "Viewport width in CSS pixels"},
				{Name: "height", Type: ArgNumber, Required: false, Description: "Viewport height in CSS pixels"},
				{Name: "device_scale_factor", Type: ArgNumber, Required: false, Description: "devicePixelRatio (default 1 or the preset's)"},
				{Name: "mobile", Type: ArgBoolean, Required: false, Description: "Mobile viewport semantics (meta viewport, screen size)"},
				{Name: "touch", Type: ArgBoolean, Required: false, Description: "Touch input emulation (pointer:coarse, maxTouchPoints)"},
				{Name: "user_agent", Type: ArgString, Required: false, Description: "Override navigator.userAgent and the User-Agent header"},
				{Name: "color_scheme", Type: ArgString, Required: false, Description: "prefers-color-scheme: dark | light | no-preference"},
				{Name: "reset", Type: ArgBoolean, Required: false, Description: "Drop every emulation override and restore the desktop viewport/UA"},
			},
			Surfaces: []string{"mcp"},
		},
		{
			Name:     "errors",
			Summary:  "Return console and network errors observed on the current page.",
			Args:     []Arg{},
			Surfaces: []string{"jsonl", "ai"},
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
		},
		{
			Name:    "extract",
			Summary: "Return a compact accessibility tree of the current page with @refs.",
			Args: []Arg{
				{Name: "level", Type: ArgString, Required: false, Description: "skeleton | content | full (default: content)"},
				{Name: "selector", Type: ArgString, Required: false, Description: "Optional CSS selector to scope extraction"},
			},
			Surfaces: []string{"jsonl", "ai"},
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
		},
		{
			Name:     "forward",
			Summary:  "Navigate forward in browser history.",
			Args:     []Arg{},
			Surfaces: []string{"jsonl", "mcp"},
		},
		{
			Name:    "hover",
			Summary: "Hover over an element by @ref (reveals dropdowns, tooltips).",
			Args: []Arg{
				{Name: "ref", Type: ArgString, Required: true, Description: "@ref of the element"},
				{Name: "snapshot", Type: ArgString, Required: false, Description: "none | diff | full (default: diff)"},
			},
			Surfaces: []string{"jsonl", "mcp", "ai"},
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
		},
		{
			Name:    "scroll_by",
			Summary: "Scroll the viewport vertically by dy pixels (positive = down).",
			Args: []Arg{
				{Name: "dy", Type: ArgInteger, Required: true, Description: "Pixels to scroll (signed)"},
			},
			Surfaces: []string{"jsonl", "ai"},
		},
		{
			Name:    "scroll_to",
			Summary: "Scroll to an absolute Y position or to the bottom of the page.",
			Args: []Arg{
				{Name: "y", Type: ArgInteger, Required: false, Description: "Absolute Y coordinate"},
				{Name: "bottom", Type: ArgBoolean, Required: false, Description: "If true, scroll to the page bottom"},
			},
			Surfaces: []string{"jsonl", "ai"},
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
		},
		{
			Name:     "url",
			Summary:  "Return the current page URL and title.",
			Args:     []Arg{},
			Surfaces: []string{"jsonl", "ai"},
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
		},
	}
}
