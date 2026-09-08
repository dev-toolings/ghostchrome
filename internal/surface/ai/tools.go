package ai

// ToolSpecs returns the tool catalog exposed to the LLM. The names match
// the JSONL ops dispatched by internal/runtime.Session.Dispatch — this
// is intentional: the LLM can read CLAUDE.md / agent docs and pick the
// same op names a human operator would.
//
// Schemas are deliberately minimal — overspec'd schemas hurt model
// recall (extra fields the model has to reason about) without buying us
// safety beyond what the agent ops already enforce at runtime.
func ToolSpecs() []ToolSpec {
	str := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	intp := func(desc string) map[string]any {
		return map[string]any{"type": "integer", "description": desc}
	}
	return []ToolSpec{
		{
			Name:        "navigate",
			Description: "Load a URL in the current tab. Use this first if the goal references a site.",
			InputSchema: object(props{
				"url":  str("Absolute URL"),
				"wait": map[string]any{"type": "string", "enum": []string{"load", "stable", "idle", "none"}},
			}, "url"),
		},
		{
			Name:        "extract",
			Description: "Return a compact accessibility tree of the current page with @refs you can click/type on. Always call this before clicking or typing on an unknown page.",
			InputSchema: object(props{
				"level":    map[string]any{"type": "string", "enum": []string{"skeleton", "content", "full"}, "description": "Default content. Use skeleton for huge pages."},
				"selector": str("Optional CSS selector to scope the extraction"),
			}),
		},
		{
			Name:        "click",
			Description: "Click an element by its @ref (from extract). Refs become stale after navigation — re-extract first.",
			InputSchema: object(props{"ref": str("@ref of the element, e.g. @5")}, "ref"),
		},
		{
			Name:        "type",
			Description: "Type text into an input identified by @ref.",
			InputSchema: object(props{"ref": str("@ref of the input"), "text": str("Text to type")}, "ref", "text"),
		},
		{
			Name:        "press",
			Description: "Press a keyboard key (e.g. Enter, Escape, ArrowDown). Optional ref to focus first.",
			InputSchema: object(props{"key": str("Key name"), "ref": str("Optional @ref to focus")}, "key"),
		},
		{
			Name:        "hover",
			Description: "Hover over an element by @ref (reveals dropdowns, tooltips).",
			InputSchema: object(props{"ref": str("@ref")}, "ref"),
		},
		{
			Name:        "select",
			Description: "Pick option(s) in a <select> by @ref.",
			InputSchema: object(props{"ref": str("@ref of <select>"), "values": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "ref", "values"),
		},
		{
			Name:        "scroll_by",
			Description: "Scroll the viewport vertically by dy pixels (positive=down).",
			InputSchema: object(props{"dy": intp("Pixels (signed)")}, "dy"),
		},
		{
			Name:        "scroll_to",
			Description: "Scroll to absolute Y or to bottom.",
			InputSchema: object(props{"y": intp("Absolute Y"), "bottom": map[string]any{"type": "boolean"}}),
		},
		{
			Name:        "eval",
			Description: "Evaluate a JavaScript expression on the page. Returns the stringified value. Use sparingly — prefer extract+click.",
			InputSchema: object(props{"expr": str("JS expression"), "ref": str("Optional @ref bound as `this`")}, "expr"),
		},
		{
			Name:        "wait",
			Description: "Wait for a CSS selector to appear or a fixed delay.",
			InputSchema: object(props{"selector": str("CSS selector"), "ms": intp("Fixed delay in ms")}),
		},
		{
			Name:        "errors",
			Description: "Return console + network errors observed on the current page.",
			InputSchema: object(nil),
		},
		{
			Name:        "url",
			Description: "Return the current page URL and title.",
			InputSchema: object(nil),
		},
		{
			Name:        "done",
			Description: "Signal that the goal is complete. Provide the answer / outcome.",
			InputSchema: object(props{"answer": str("Final answer or short summary of the outcome")}, "answer"),
		},
	}
}

type props map[string]any

func object(p props, required ...string) map[string]any {
	out := map[string]any{
		"type": "object",
	}
	if p == nil {
		out["properties"] = map[string]any{}
	} else {
		out["properties"] = map[string]any(p)
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// SystemPrompt is the role-priming message prepended to every conversation.
// Keep it tight — verbose system prompts hurt latency and cost.
const SystemPrompt = `You are an autonomous browser-driving agent. You control a real Chrome
session via a fixed set of tools. Your job is to accomplish the user's
GOAL by calling those tools.

Rules:
- After navigation or any structural change, call "extract" before clicking
  or typing — refs go stale.
- Prefer the smallest sequence of tool calls. Don't narrate; act.
- If you're confident the goal is achieved, call the "done" tool with a
  concise "answer" describing the outcome (e.g. the price you found, the
  fact that the form was submitted, etc.).
- If you hit a captcha / DataDome / Cloudflare interstitial (visible in the
  observation's captcha_hint), stop and call "done" with answer="blocked: <reason>".
- Never invent @refs. Only use refs returned by a recent "extract".
`
