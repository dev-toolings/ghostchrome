# MCP server reference

ghostchrome ships an MCP (Model Context Protocol) server that exposes the
browser as 19 stdio tools to LLM agents — Claude Code, Codex, Cursor, the
Anthropic Agent SDK, OpenAI Agents SDK, or any other MCP host. One
long-lived Chrome + page is shared across tool calls so refs (`@1`, `@2`)
extracted by one tool remain valid for the next.

Available as a subcommand (`ghostchrome mcp`) or as a standalone binary
(`ghostchrome-mcp`) configured via `GHOSTCHROME_*` environment variables.

- Protocol: **MCP 2025-11-25**
- Transport: **stdio** (JSON-RPC over stdin/stdout)
- Tool count: **19**

---

## Wire-up

Install ghostchrome (one-time):

```bash
curl -fsSL https://raw.githubusercontent.com/dev-toolings/ghostchrome/main/scripts/install.sh \
  | bash -s -- --mode cli
```

Add it to your agent (one-time):

```bash
# Claude Code (Anthropic)
claude mcp add ghostchrome -- ghostchrome mcp --stealth

# Codex (OpenAI)
codex mcp add ghostchrome -- ghostchrome mcp --stealth

# Cursor — add to settings.json mcpServers:
#   "ghostchrome": { "command": "ghostchrome", "args": ["mcp", "--stealth"] }
```

The flag `--stealth` enables the bundled fingerprint patches + script
blocker. Drop it for plain headless Chrome. All global ghostchrome flags
work with `mcp`:

| Flag | Effect |
|---|---|
| `--connect=auto` | Attach to an existing Chrome on `127.0.0.1:9222-9229` in a fresh tab. Lets multiple agents share one Chrome. |
| `--connect=ws://...` | Attach to a specific Chrome DevTools endpoint. |
| `--headless=false` | Show a window. Useful for debugging the agent. |
| `--user-profile NAME` | Persist cookies/localStorage under `~/.ghostchrome/profiles/NAME/`. |
| `--proxy http://...` | Route all requests through a proxy (basic auth supported). |
| `--dismiss-cookies` | Auto-dismiss EU/CCPA consent banners on every navigation. |
| `--timeout N` | Per-tool deadline (seconds, default 30). |
| `--block-trackers` | Network-layer block of DataDome, PerimeterX, etc. Auto-on with `--stealth`. |

The server prewarms Chrome in the background while the MCP client is
still negotiating capabilities, so the first user-facing tool call
doesn't pay the ~1.3s cold-start. Set `GHOSTCHROME_MCP_LAZY=1` to defer
the spawn until the first tool call instead.

---

## Tool reference

All tools return MCP content blocks. Text-result tools emit one
`TextContent`; the snapshot tool emits a one-line human header followed
by structured JSON on the next line so the model gets both at once.
The screenshot tool emits a text summary + one `ImageContent`.

Refs (`@1`, `@2`, ...) come from the latest `snapshot` call. They stay
valid until the next navigation or snapshot. The CLI accepts `@3`, `3`,
or `ref:3` interchangeably and normalizes to `@3`.

---

### `snapshot`

Page status + console + network + DOM with refs. The canonical first
call when an agent visits or revisits a page. Fuses what the CLI does as
`preview` + `extract` + `errors` into one tool result.

| Param | Type | Default | Description |
|---|---|---|---|
| `url` | string | — | Optional. If provided, navigate first; otherwise snapshot the current page. |
| `wait` | enum | `domcontentloaded` | One of `domcontentloaded`, `load`, `stable`, `idle`, `none`. |
| `level` | enum | `content` | DOM depth: `skeleton` (interactive only), `content` (adds text), `full` (everything named). |
| `selector` | string | — | Optional CSS scope for the DOM extract. |

**Output** (text content):

```
[200] Example Domain — https://example.com/ | 0 errors, 0 failed reqs, 1 interactive
{"page":{"url":"...","title":"...","status":200,"time_ms":79},"errors":[],"network":[],"dom":{...},"summary":{...}}
```

Use the header for the model's quick read; parse the JSON line for
structure. The DOM section embeds refs:

```
hdr
 nav
  @1 a>/home Home
  @2 a>/docs Docs
 @3 b Sign in
main
 h1 Welcome
 @4 b Get started
```

---

### `navigate`

Go to a URL without a full snapshot. Returns only status + title + URL +
load time. Use when chaining navigations and you don't need the DOM yet.

| Param | Type | Default | Description |
|---|---|---|---|
| `url` | string | **required** | Absolute URL. |
| `wait` | enum | `domcontentloaded` | Same set as `snapshot`. |

**Output**: `[200] Title — URL (134ms)` + JSON page info.

---

### `click`

Click an element by ref. Auto-waits for the element to be attached,
visible, stable, and enabled. Returns a one-line ack plus a compact a11y-ref diff (`unchanged` when the tree did not change).

| Param | Type | Default | Description |
|---|---|---|---|
| `ref` | string | **required** | `@3` or `3` — ref from the last snapshot. |

---

### `type`

Type text into an input/textarea. The field is cleared first. Set
`submit: true` to press Enter after typing — covers the common
fill-and-submit pattern in one call.

| Param | Type | Default | Description |
|---|---|---|---|
| `ref` | string | **required** | Target input by ref. |
| `text` | string | **required** | Text to type. |
| `submit` | boolean | `false` | Press Enter after typing. |

**Output**: `typed into @N (K chars)` plus a compact a11y-ref diff.

---

### `select`

Pick one or more options in a `<select>`. Pass a single option value or
a JSON-stringified array for multi-select.

| Param | Type | Default | Description |
|---|---|---|---|
| `ref` | string | **required** | The `<select>` element. |
| `value` | string | **required** | Option `value` or visible label. JSON array string for multi-select: `"[\"a\",\"b\"]"`. |

---

### `press`

Send a keyboard key. Names follow the [Chrome DevTools key
catalogue](https://developer.mozilla.org/docs/Web/API/UI_Events/Keyboard_event_key_values):
`Enter`, `Tab`, `Escape`, `ArrowUp`/`ArrowDown`/`ArrowLeft`/`ArrowRight`,
`PageDown`, `PageUp`, `Home`, `End`, `Backspace`, `Delete`, `F1`-`F12`,
single characters (`a`, `1`, ...).

| Param | Type | Default | Description |
|---|---|---|---|
| `key` | string | **required** | Key name. |
| `ref` | string | — | Optional. Focus this element before pressing. |

---

### `wait_for`

Wait for a condition before continuing. At least one of `selector`,
`text`, or a timeout-only call is required.

| Param | Type | Default | Description |
|---|---|---|---|
| `selector` | string | — | Wait for this CSS selector to appear. |
| `text` | string | — | Wait for this visible text substring on the page. |
| `timeout_ms` | number | `5000` | Max wait. Capped at 30000. |

Behavior:

- `selector` + `timeout_ms` → poll for the selector via Rod's wait, fail
  with `wait_for selector: timeout` on miss.
- `text` + `timeout_ms` → poll `body.innerText` every 100 ms; substring
  match (case-sensitive).
- No `selector`/`text` → plain `time.Sleep(timeout_ms)`. Useful when an
  SPA needs a beat to settle but no specific selector exists yet.

---

### `eval`

Evaluate a JavaScript expression in the page context. Async expressions
are awaited automatically. Returns the serialized value as text.

| Param | Type | Default | Description |
|---|---|---|---|
| `expression` | string | **required** | JS expression. Top-level `await` OK. |
| `ref` | string | — | If set, scope `this` to this element. |
| `timeout_ms` | number | `8000` | Per-call deadline. |

Escape hatch for everything the other tools can't do: read
`localStorage`, dispatch synthetic events, query computed styles,
trigger custom JS APIs. Has a one-shot auto-recovery (wait + retry) when
the main thread is busy with anti-bot scripts — surfaces a clear
timeout error otherwise instead of blocking forever.

---

### `hover`

Hover over an element by ref. Triggers CSS `:hover` states and any
hover-bound JS listeners.

| Param | Type | Default | Description |
|---|---|---|---|
| `ref` | string | **required** | Element ref from the last snapshot. |

---

### `drag`

Drag an element from one ref to another. Simulates a full mouse
drag-and-drop sequence with configurable step count.

| Param | Type | Default | Description |
|---|---|---|---|
| `from` | string | **required** | Source element ref. |
| `to` | string | **required** | Target element ref. |
| `steps` | number | `10` | Intermediate mouse move steps. |

---

### `swipe`

Single-finger touch gesture between two viewport coordinates, in CSS
pixels: `touchstart` → N × `touchmove` → `touchend`, dispatched with
`Input.dispatchTouchEvent`.

Use it — not `drag` — for a mobile drawer, carousel, bottom sheet, or
pull-to-refresh. `drag` synthesizes **mouse** events, which a touch-only
handler never receives. Touch emulation is turned on automatically when
the page has none, so a swipe is never silently discarded.

| Param | Type | Default | Description |
|---|---|---|---|
| `from_x` | number | **required** | Start X in CSS pixels, relative to the viewport. |
| `from_y` | number | **required** | Start Y in CSS pixels. |
| `to_x` | number | **required** | End X. |
| `to_y` | number | **required** | End Y. |
| `duration_ms` | number | `300` | Gesture duration (max 10000). Short = flick, long = slow drag. |
| `steps` | number | `12` | Intermediate `touchmove` events (max 100). |
| `snapshot` | string | `diff` | `none` \| `diff` \| `full`. |

```jsonc
// open a left drawer with an edge swipe
{ "from_x": 8, "from_y": 500, "to_x": 300, "to_y": 500, "duration_ms": 250 }
```

---

### `emulate`

Device emulation: viewport metrics, `devicePixelRatio`, the mobile flag,
touch input, user-agent, and `prefers-color-scheme`. Without it the page
is a 1920×1080 desktop with `pointer: fine`, so a phone breakpoint or a
coarse-pointer media query never activates.

The profile is held by the server and replayed whenever it binds a new
page — a tab switch, a popup, a crash relaunch — because CDP emulation
overrides die with the target.

| Param | Type | Default | Description |
|---|---|---|---|
| `device` | string | — | Preset: `iphone-se`, `iphone-14`, `iphone-14-pro`, `iphone-14-pro-max`, `pixel-7`, `pixel-8-pro`, `ipad`, `ipad-pro`, `desktop`, `desktop-2k`. |
| `width` | number | — | Viewport width in CSS pixels (overrides the preset). |
| `height` | number | — | Viewport height in CSS pixels. |
| `device_scale_factor` | number | `1` | `devicePixelRatio` (0.1–5). |
| `mobile` | boolean | preset | Mobile viewport semantics (meta viewport, screen size). |
| `touch` | boolean | preset | Touch input: `pointer: coarse`, `navigator.maxTouchPoints`. |
| `user_agent` | string | preset | Override `navigator.userAgent` and the UA header. |
| `color_scheme` | string | — | `dark` \| `light` \| `no-preference`. |
| `reset` | boolean | `false` | Drop every override, restore the real viewport and UA. |

```jsonc
{ "device": "iphone-14-pro-max" }
{ "width": 430, "height": 932, "device_scale_factor": 3, "mobile": true, "touch": true }
{ "reset": true }
```

An emulation change relayouts the document: take a fresh `snapshot`
before using any ref.

---

### `fill_form`

Fill multiple form fields in one call. Pass a JSON object mapping refs
to text values.

| Param | Type | Default | Description |
|---|---|---|---|
| `fields` | string | **required** | JSON object: `{"@1": "John", "@2": "john@example.com"}`. |

---

### `upload`

Upload file(s) to a file input element. Gated by policy `allow_upload`.

| Param | Type | Default | Description |
|---|---|---|---|
| `ref` | string | **required** | File input element ref. |
| `paths` | string | **required** | File path or JSON array of paths. |

---

### `tabs`

List, switch, or close browser tabs.

| Param | Type | Default | Description |
|---|---|---|---|
| `action` | enum | `list` | `list`, `switch`, `close`. |
| `index` | number | — | Tab index (required for `switch`/`close`). |

---

### `screenshot`

Capture the page (or one element) as an image embedded in the MCP
result. The model receives the image directly via `ImageContent`.

| Param | Type | Default | Description |
|---|---|---|---|
| `ref` | string | — | Capture only this element. |
| `full_page` | boolean | `false` | Capture the full scrollable page instead of the viewport. |
| `format` | enum | `webp` | `webp`, `jpeg`, `png`. |
| `quality` | number | `60` | 1-100 for `webp`/`jpeg`. Ignored for `png`. |
| `annotate` | boolean | `false` | Overlay numbered borders on interactive elements (forces PNG). |

WebP at quality 60 is typically 30-50% lighter than equivalent JPEG and
~70% lighter than PNG for UI captures — choose `png` only when pixel
exactness matters. Use `annotate: true` for vision-based agent workflows
where the model needs to see which elements correspond to which refs.

---

### `back` / `forward`

Browser history navigation. No arguments. Waits until the page reaches a
stable state before returning.

**Output**: `navigated back to <URL>` or `navigated forward to <URL>`.

---

## A complete agent loop

```jsonc
// 1. First read of an unknown page
{ "method": "tools/call", "params": {
    "name": "snapshot",
    "arguments": { "url": "https://example.com" } } }
// → "[200] Example Domain — https://example.com/ | 0 errors..."
//   dom shows "@1 a>https://www.iana.org/...  More information..."

// 2. Click the link
{ "method": "tools/call", "params": {
    "name": "click", "arguments": { "ref": "@1" } } }

// 3. Snapshot the new page (refs renumber)
{ "method": "tools/call", "params": { "name": "snapshot", "arguments": {} } }

// 4. Fill a form
{ "method": "tools/call", "params": {
    "name": "type",
    "arguments": { "ref": "@3", "text": "alice@example.com", "submit": true } } }

// 5. Wait for the success page
{ "method": "tools/call", "params": {
    "name": "wait_for",
    "arguments": { "text": "Thanks for signing up", "timeout_ms": 5000 } } }
```

---

## Why 19 tools (not 38)

Earlier versions of the MCP server exposed 38 tools (cookies + storage +
viewport + dialogs + network sniffing + trace recording + diagnostics).
Every entry shows up in the `tools/list` the model sees on every session
and creates ambiguity.

v2.0 ships 19 tools that cover the complete agent loop: navigate,
observe, interact (click/type/hover/drag/swipe/fill/upload/select/press),
emulate a device, manage tabs, wait, eval, and screenshot (with
annotation). Niche
workflows live in the **CLI** — reach them via the `eval` escape hatch
or by shelling out to `ghostchrome <command>`.

---

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| First call hangs ~3s | Chrome cold-start. Set `GHOSTCHROME_MCP_LAZY=0` (default) to prewarm; passing `--connect=auto` skips spawn entirely. |
| `snapshot` returns `[403]` or `[503]` | Anti-bot challenge. Make sure `--stealth` is on; if the site uses DataDome the script blocker (auto-on with stealth) is what unblocks it. |
| `click @3` says ref not found | The previous tool was `navigate` (not `snapshot`) — `navigate` doesn't refresh refs. Call `snapshot` first, or use `snapshot` with the URL instead of `navigate`. |
| `eval` times out at 8000 ms | Heavy fingerprinting script holding the main thread. Try `eval` again immediately (the auto-recovery often succeeds on the second pass) or bump `timeout_ms`. |
| Screenshot is huge | Default `webp q60` is already lean; lowering `quality` to 30-40 typically halves the size with no perceptible loss for UI captures. |
| Different agents stomp on each other's tabs | Run one `ghostchrome serve` and pass `--connect=auto` to each agent. Each will land in a fresh background tab in the same Chrome. |
