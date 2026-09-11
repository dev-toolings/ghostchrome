# MCP reference

Read this reference only after the installation manifest selects `mcp`. Use the
registered Ghostchrome MCP connection; do not invoke the CLI or spawn a second
MCP process from a shell. Setup renders the client configuration with the stable
installed executable, normally `~/.ghostchrome/bin/ghostchrome-mcp`.

## Tool flow

Start with `snapshot` when entering or revisiting a page. It returns page
metadata, console/network observations, and a compact accessibility tree with
refs. Use a fresh snapshot after navigation, a route transition, a modal, or a
DOM-changing interaction.

The 19-tool surface is:

```text
snapshot, navigate, click, type, select, press, hover, drag, swipe, emulate,
fill_form, upload, tabs, dialog, wait_for, eval, screenshot, back, forward
```

Use refs from the current snapshot for interaction. Validate each mutating tool
call through a URL/title change, a control state, visible text, or a new
snapshot. Use `wait_for` with a selector or state condition for dynamic pages;
prefer a bounded condition over arbitrary sleeps. Use `screenshot` for visual
evidence, not as a replacement for semantic extraction.

## Device emulation and touch gestures

The server's default context is a desktop tab: a 1920x1080 viewport, a
`devicePixelRatio` of 1, and `pointer: fine`. A mobile shell, a phone
breakpoint, or a coarse-pointer media query therefore stays inactive until
`emulate` installs a device profile.

```jsonc
// iPhone 14 Pro Max, by preset
{"device": "iphone-14-pro-max"}
// the same geometry, stated explicitly
{"width": 430, "height": 932, "device_scale_factor": 3, "mobile": true, "touch": true,
 "user_agent": "Mozilla/5.0 (iPhone; CPU iPhone OS 17_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3 Mobile/15E148 Safari/604.1"}
// iPhone 15 Pro Max as an installed (standalone) PWA: Dynamic Island + home indicator
{"device": "iphone-14-pro-max", "safe_area_top": 59, "safe_area_bottom": 34}
// back to an un-emulated desktop tab
{"reset": true}
```

Presets are `iphone-se`, `iphone-14`, `iphone-14-pro`, `iphone-14-pro-max`,
`pixel-7`, `pixel-8-pro`, `ipad`, `ipad-pro`, `desktop`, and `desktop-2k`.
Individual axes override the preset, and `color_scheme` emulates
`prefers-color-scheme`. `safe_area_top`, `safe_area_right`, `safe_area_bottom`
and `safe_area_left` set what `env(safe-area-inset-*)` resolves to, in CSS
pixels; an absent axis keeps its value and an explicit 0 clears it. The reply
says `via cdp` when the browser honours `Emulation.setSafeAreaInsetsOverride`
and `via css-rewrite` on an older Chromium, where every same-origin style rule
that references the insets is copied with the values substituted (a
cross-origin stylesheet stays untouched). A landscape phone swaps the values:
top and bottom become the sides. The profile survives a tab switch, a popup, and a
browser relaunch, because the server replays it whenever it binds a new page.
An emulation change relayouts the document: take a fresh snapshot before using
any ref, and reset when the mobile part of the flow is finished.

`swipe` dispatches a real single-finger touch sequence — `touchstart`, a series
of `touchmove` events, then `touchend` — between two viewport coordinates in
CSS pixels. Use it for a drawer, a carousel, a bottom sheet, or pull-to-refresh:
`drag` sends mouse events only, which a touch-only handler never receives.

```jsonc
// open a left drawer: swipe right from the screen edge
{"from_x": 8, "from_y": 500, "to_x": 300, "to_y": 500, "duration_ms": 250}
// scroll a list with a flick
{"from_x": 215, "from_y": 700, "to_x": 215, "to_y": 200, "duration_ms": 150, "steps": 20}
```

A short duration reads as a flick and can trigger momentum; a longer one reads
as a deliberate drag. Touch emulation is enabled automatically when a swipe is
requested on a page that has none, so the gesture is never silently discarded.

There are no recipes, CLI session commands, `perf`, `capture`, or JSONL batch
operations on this MCP surface. Request an explicit CLI-mode setup switch when
one of those capabilities is required; never call an unregistered checkout as a
fallback.

## Configuration shape

The setup command writes the equivalent of this configuration for MCP-capable
clients. Replace the placeholder with the absolute path emitted by
`ghostchrome setup status`; never point at a repository build.

```toml
[mcp_servers.ghostchrome]
command = "/home/user/.ghostchrome/bin/ghostchrome-mcp"
args = ["--stealth"]
```

Claude Code and other clients may store the same command in a JSON or their own
configuration format. Let setup render those formats and preserve unmanaged
fields. A mode switch removes only the Ghostchrome entry that setup owns.

## Runtime controls

Configure the standalone server through environment variables when setup or the
client supports them:

| Variable | Purpose |
| --- | --- |
| `GHOSTCHROME_CONNECT` | Attach to a local or explicit CDP endpoint instead of spawning Chrome. |
| `GHOSTCHROME_HEADLESS` | Select headless or visible Chrome. |
| `GHOSTCHROME_STEALTH` | Enable anti-fingerprint patches when policy permits. |
| `GHOSTCHROME_PROFILE` | Select persistent profile storage. |
| `GHOSTCHROME_TIMEOUT` | Bound individual tool operations. |
| `GHOSTCHROME_MCP_LAZY` | Defer browser launch until the first tool call. |
| `GHOSTCHROME_IDLE_TIMEOUT` | Release idle Chrome while retaining the stdio server. |

The default idle timeout is 15 minutes. The MCP process remains available while
the browser is released; the next browser tool call recreates the context. This
keeps idle RSS bounded without requiring a client re-registration.

## Lifecycle and protocol hygiene

Keep JSON-RPC protocol traffic on stdin/stdout. Treat stderr as diagnostics and
never parse it as tool output. Allow the client to close stdin when the flow is
complete. On graceful shutdown, the server closes its page, browser, and profile
handles. If a server appears stuck, inspect `ghostchrome setup doctor
--strict` and the client process tree before stopping it; do not kill unrelated
Chrome instances.

Each MCP client should hold one Ghostchrome connection. Duplicate registrations
can create one browser per server and inflate memory. Remove stale managed
entries through `ghostchrome setup switch --to mcp --yes`, not by deleting
arbitrary client configuration sections.

## MCP failures

For a missing tool connection, report the client and mode mismatch and run the
setup doctor. For a tool timeout, retain the last snapshot, check `errors`, and
retry only an idempotent operation once when the page is still reachable. Avoid
repeating form submissions, uploads, purchases, or destructive actions without
fresh confirmation. For a browser launch failure, report the missing executable
or native dependency and stop; do not silently switch transports.
