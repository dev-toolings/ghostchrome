# CLI reference

Every ghostchrome command, grouped by execution path. All commands accept
the global flags listed [at the bottom](#global-flags). Run `ghostchrome
<command> --help` for the full per-command flag list.

By default every command auto-launches a temporary Chrome and shuts it
down on exit. Pass `--connect=auto` (after running `ghostchrome serve`
once) to share one long-lived Chrome across calls — recommended for
agent loops and bulk scraping.

Playwright CLI-compatible command names are tracked in
[`playwright-cli-parity.md`](playwright-cli-parity.md). That page marks each
command as **compatible**, **partial**, or **gap**.

Mapped command names currently include:
`open`, `snapshot`, `fill`, `resize`, `go-back`, `go-forward`,
`state-save`, `state-load`, `attach <session-name>`, `attach --cdp=<channel|url>`,
`cookie-*`, `localstorage-*`, `sessionstorage-*`, `dialog-*`, `tab-*`, `list`,
`close`, `close-all`, `kill-all`, `delete-data`, `console`, `network`, `route`,
`network-state-set`, `mousemove`, `mousedown`, `mouseup`, `mousewheel`,
`keydown`, `keyup`, `verify-*`, `generate-locator`, `config-print`,
`install --skills`, and `show`.

- [HTTP fast path](#http-fast-path)
- [Page inspection](#page-inspection)
- [Interaction](#interaction)
- [Waits and assertions](#waits-and-assertions)
- [History](#history)
- [Browser and session](#browser-and-session)
- [Automation and scraping](#automation-and-scraping)
- [Network capture and replay](#network-capture-and-replay)
- [Global flags](#global-flags)

---

## HTTP fast path

No Chrome by default. Hits the URL directly with realistic browser
headers. ~10× faster than spawning a browser when the site renders
server-side or exposes JSON APIs.

### `fastfetch <url>`

GET an HTML page, detect anti-bot challenges, extract SSR data islands
(Next.js `__NEXT_DATA__`, Nuxt `__NUXT_DATA__`, hydration JSON, etc.).
Falls back to a browser when `--fallback-browser` is set and the
response looks blocked or empty.

| Flag | Default | Effect |
|---|---|---|
| `--ua "..."` | runtime-aware Chrome 135 | Override User-Agent |
| `--accept-language "..."` | `fr-FR,fr;q=0.9,...` | Override Accept-Language |
| `--header K=V` | — | Extra header (repeatable, `K:V` also accepted) |
| `--timeout-ms N` | 8000 | HTTP deadline |
| `--raw` | off | Output the HTML body instead of the JSON envelope |
| `--next-data` | off | Output only the parsed `__NEXT_DATA__` JSON |
| `--include-payloads` | off | Embed every SSR payload in the envelope |
| `--fallback-tls` | off | Retry blocked/SSR-empty GET once with Chrome 146 TLS + HTTP/2, within the HTTP deadline |
| `--fallback-browser` | off | Spawn Chrome and re-extract on Blocked or empty SSR |
| `--pretty` | off | Pretty-print JSON output |
| `--output PATH` | stdout | Write to file |

### `fetchapi <url>`

Issue a JSON API request with browser headers. Useful for the JSON
endpoints behind SPAs once `sniff-api` has revealed them.

| Flag | Default | Effect |
|---|---|---|
| `--method METHOD` | `GET` | HTTP verb |
| `--body JSON` | — | Request body (JSON literal or `@path/to/file`) |
| `--header K=V` | — | Extra header (repeatable) |
| `--timeout-ms N` | 8000 | HTTP deadline |
| `--pretty` | off | Pretty-print JSON output |

---

## Page inspection

### `open [url]`

Open a browser, optionally navigate to a URL, and return page state plus a
snapshot. This is the Playwright CLI-compatible entrypoint over ghostchrome's
existing browser/session model.

| Flag | Effect |
|---|---|
| `--headed` | Global Playwright CLI-compatible inverse of `--headless` |
| `--browser=chrome\|chromium` | Select Chrome/Chromium launch; other Playwright browser names are rejected explicitly |
| `--persistent` | Use persistent ghostchrome profile `default` unless another profile is selected |
| `--profile PATH` | Use a custom browser user data directory for this `open` command |
| `--config PATH` | Load mappable fields from a Playwright CLI JSON config |

### `preview <url>`

All-in-one page health report: status, console + network errors, request
count, compact DOM with refs. The first call when investigating a page.

| Flag | Default | Effect |
|---|---|---|
| `--level LEVEL` | `content` | `skeleton`, `content`, `full` |
| `--wait STRATEGY` | `domcontentloaded` | `domcontentloaded`, `load`, `stable`, `idle`, `none` |
| `--selector CSS` | — | Scope DOM extract to the subtree of the first match (falls back to the full page with a warning on stderr when it resolves to nothing) |
| `--no-network` | off | Skip the network section |

### `navigate <url>`

Go to a URL and optionally extract the DOM. Output: status, title, final
URL, time.

| Flag | Default | Effect |
|---|---|---|
| `--wait STRATEGY` | `domcontentloaded` | Same set as `preview` |
| `--extract LEVEL` | — | If set, extract DOM at this level after navigating |

### `extract [url]`

Compact accessibility tree with refs. URL is optional when reused with
`--connect=auto` against an already-open page.
Alias: `snapshot`. With the alias, a positional `@ref` scopes output to that
element's subtree; Playwright-style `eN` refs and CSS selectors are accepted.
`snapshot` renders a Playwright-like tree by default and `--raw` omits page
metadata.
Snapshot-producing Playwright-compatible commands write timestamped
`.playwright-cli/page-*.yml` artifacts in text mode and print a `Snapshot`
link.

| Flag | Default | Effect |
|---|---|---|
| `--level LEVEL` | `content` | `skeleton`, `content`, `full` |
| `--selector CSS` | — | Scope to the subtree of the first match (falls back to the full page with a warning on stderr when it resolves to nothing) |
| `--filename PATH` | — | Write the snapshot payload to a file |
| `--depth N` | `-1` | Limit tree depth (`-1` = unlimited) |
| `--raw` | off | Return only the snapshot tree for `snapshot` |

### `errors [url]`

Console + browser-side Log domain (CORS, CSP, mixed content, network
ERR_*) + HTTP 4xx/5xx in one report.

| Flag | Default | Effect |
|---|---|---|
| `--level all\|error\|warning` | `error` | Filter severity |
| `--with-network` | on | Include network 4xx/5xx |

### `console [level|url] [url]`

Playwright CLI-compatible active observer for console messages. It reports
events captured while the command is running, and appends them to a bounded
session buffer when using `-s` or `--connect`.

| Flag | Default | Effect |
|---|---|---|
| `--level all\|error\|warning\|debug` | `all` | Filter console severity |
| `--wait N` | `0` | Seconds to keep observing after optional navigation |
| `--clear` | off | Clear the current command buffer and return no entries |

### `screenshot [url|ref]`

PNG/JPEG/WebP of the viewport, full scrollable page, or one element.

| Flag | Default | Effect |
|---|---|---|
| `--full` | off | Capture full scrollable page |
| `--element @ref` | — | Capture one element |
| `--format webp\|jpeg\|png` | `webp` | Image format |
| `--quality 1-100` | 60 | Quality for webp/jpeg |
| `--output PATH` | `screenshot.<ext>` | Output file |

### `eval [expression] [url|ref]`

Run a JS expression in the page, await async values, return the
serialized result.

| Flag | Default | Effect |
|---|---|---|
| `--on @ref` | — | Scope `this` to this element |
| `--timeout-ms N` | 8000 | Per-call deadline |
| `--expr "..."` | — | Alternative to positional argument |

### `run-code <code>` / `run-code --filename script.js`

Playwright CLI compatibility boundary. Playwright's `run-code` executes
arbitrary scripts with full Playwright `page/context` API access; ghostchrome
does not embed a Playwright runtime, so this command returns an unsupported
error. Use `eval` for JavaScript executed in the current page context.

### `perf <url>`

Lighthouse-lite timing summary: navigation, paint, network breakdown.
Lower granularity than DevTools' panel, fits agent output.

### `pdf [url]`

Print the page to PDF.

| Flag | Default | Effect |
|---|---|---|
| `--output PATH` | `page.pdf` | Output file |
| `--landscape` | off | Landscape orientation |
| `--scale N` | 1.0 | Print scale |

---

## Interaction

Refs (`@1`, `@2`, ...) come from the latest snapshot. Each interactive
command navigates if you pass a URL, or operates on the current page
with `--connect=auto`.

### `click <ref|url> [url]`

Click an element. Auto-waits for attached + visible + stable + enabled.
After the click, prints a compact a11y-ref diff by default.

| Flag | Default | Effect |
|---|---|---|
| `--snapshot MODE` | `diff` | `diff` (compact ref changes), `full` (Playwright page-state dump), `none` (ack only) |
| `--wait-for STATE` | `visible` | `attached`, `visible`, `hidden`, `enabled`, `stable`, `none` |
| `--wait-timeout-ms N` | `5000` | Max wait for the element state (`0` disables) |

### `dblclick <ref> [url]`

Double-click an element by `@ref`.

### `check <ref> [url]` / `uncheck <ref> [url]`

Tick / untick a checkbox or radio by `@ref`. **Idempotent**: reads the
current `checked` state first and does nothing if already in the target
state, so `check` never accidentally toggles a box that was already ticked.

### `type <text>` / `type <ref> <text> [url]`

Type into the focused element when only text is provided. With a ref or
semantic locator, focus the target input/textarea, clear it, and fill text.

| Flag | Default | Effect |
|---|---|---|
| `--submit` | off | Press Enter after typing |
| `--snapshot MODE` | `diff` | `diff` / `full` / `none` post-action snapshot |
| `--by-role`, `--by-name`, `--by-label`, `--by-text` | — | Target by semantic locator instead of ref |

### `press <key> [url]`

Press a keyboard key. Names: `Enter`, `Tab`, `Escape`, `ArrowUp/Down`,
`PageDown`, `F1`-`F12`, single chars.

| Flag | Default | Effect |
|---|---|---|
| `--on @ref` | — | Focus element before pressing |

### `hover [ref|url] [url]`

Mouse-hover an element. Useful for menus, tooltips, on-hover content.

### `select <ref> <value> [url]`

Pick an option in a `<select>`. For multi-select, pass a comma-separated
list or repeat `--value`.

### `fill-form [json]`

Bulk fill fields from a JSON object `{"@3": "alice", "@4": "secret"}`.
Less round-trips than individual `type` calls.

### `scroll <ref>` / `scroll-by <dy>` / `scroll-to <target>`

Scroll variants. `scroll <ref>` scrolls an element into view; `scroll-by
<dy>` scrolls the page by Y pixels; `scroll-to <target>` accepts `top`,
`bottom`, or a Y pixel value.

### `upload [ref] <file> [file2 ...]`

Upload one or more files into an `<input type=file>` by ref.

---

## Waits and assertions

### `waitfor [css-selector] [url]`

Block until a CSS selector appears. Use `--text "..."` for visible-text
waiting instead. Useful in CI scripts.

| Flag | Default | Effect |
|---|---|---|
| `--timeout N` | 30 | Seconds to wait |
| `--text "..."` | — | Wait for substring instead of selector |

### `wait-port <port>`

Block until a TCP port accepts a connection. Useful in pipelines:

```bash
bun dev &
ghostchrome wait-port 3000 --timeout 30 && \
  ghostchrome preview http://localhost:3000
```

### `wait-url <url>`

Poll a URL via HTTP (no browser) until it returns the expected status.

| Flag | Default | Effect |
|---|---|---|
| `--status N` | 200 | Expected HTTP status |
| `--timeout N` | 30 | Seconds to wait |
| `--insecure` | off | Skip TLS verification |

### `assert <subcommand> <url>`

Composable page assertions for CI/smoke tests. Exit non-zero on
mismatch, useful with `&&` chaining.

| Subcommand | Checks |
|---|---|
| `selector-visible <css>` | The CSS selector is present and visible |
| `text <substring>` | Visible page text contains the substring |
| `title-matches <pattern>` | `document.title` matches a regex |
| `url-matches <pattern>` | Final URL matches a regex |
| `no-console-errors` | Zero `console.error` events captured |
| `no-network-4xx` | Zero failed (4xx/5xx) requests |
| `count <selector>` | DOM element count matches `--eq`, `--gte`, `--lte` |

### Playwright CLI testing commands

Top-level testing commands matching the Playwright CLI capability names. They
exit `0` when the condition is proven and `1` when it is not. Each accepts
`--url` to navigate first; otherwise it checks the current page/session.

| Command | Checks |
|---|---|
| `verify-element-visible [role] [name]` | A semantic element is visible; also supports `--by-role`, `--by-name`, `--by-label`, `--by-text` |
| `verify-text-visible <text>` | Visible page text contains `text` |
| `verify-list-visible <item> [item...]` | A visible `ul`, `ol`, or `[role=list]` contains all provided items |
| `verify-value [target] <expected>` | A form field value equals `expected`; target can be @ref/eN, CSS selector, or omitted with `--by-*` |
| `generate-locator [ref\|text]` | Emit a Playwright locator suggestion (`getByRole`, `getByText`, or `getByLabel`) |

---

## History

### `back` / `forward`

Navigate back/forward in browser history. Waits for the page to stabilize.

### `reload`

Refresh the current page. Waits for the `load` lifecycle event (not full
network idle), so pages with persistent analytics/chat connections still
return instead of timing out.

### `dialog`

Manage native `alert/confirm/prompt` dialogs.

| Subcommand | Effect |
|---|---|
| `accept` | Accept the next dialog |
| `dismiss` | Dismiss the next dialog |
| `set <name=value>` | Pre-set the prompt text |

---

## Browser and session

### `sessions` (and the `-s` flag)

The ergonomic, playwright-cli-style way to keep a browser alive. `-s <name>`
(or `$PLAYWRIGHT_CLI_SESSION`, falling back to `$GHOSTCHROME_SESSION`) on
**any** command auto-launches a persistent Chrome on first use, bound to a disk
profile of the same name (cookies persist under
`~/.ghostchrome/profiles/<name>`), and reuses it — including the active tab —
across calls. No `ws://` URL to manage.

```bash
ghostchrome -s work goto https://example.com   # spawn on first use
ghostchrome -s work click @3                   # reuse, state persists
GHOSTCHROME_SESSION=work ghostchrome extract    # env var = default session
PLAYWRIGHT_CLI_SESSION=work ghostchrome extract # Playwright CLI-compatible env var
```

| Subcommand | Effect |
|---|---|
| `sessions list` | Show sessions with port, PID, liveness |
| `sessions stop <name> [--purge]` | Terminate a session's Chrome; `--purge` also deletes its profile |
| `sessions prune` | Drop dead sessions (Chrome unreachable) from the registry |
| `sessions kill-all [--purge]` | Stop every session; `--purge` also deletes each profile |

Playwright CLI-compatible aliases: `list`, `close [name]`, `close-all`,
`kill-all`, `delete-data [name]`.

### `profiles`

Persistent Chrome profiles (`~/.ghostchrome/profiles/<name>`) store cookies and
cache so logins/sessions survive. They accumulate disk — list and remove them.

| Subcommand | Effect |
|---|---|
| `profiles list` | Show profiles sorted by on-disk size (+ total) |
| `profiles rm <name> [name...]` | Delete profiles and reclaim their disk |

### `uninstall`

Stop all sessions and remove the ghostchrome binary. `--purge` also deletes the
data directories (profiles, sessions, contexts, cache). Dry-run unless `--yes`.

```bash
ghostchrome uninstall                # print what would be removed
ghostchrome uninstall --yes          # remove the binary + bundled skill
ghostchrome uninstall --purge --yes  # remove the binary, skill, AND all data
```

### `skills`

ghostchrome bundles an agent skill (teaches Claude Code how to drive it) inside
the binary. The install script installs it globally; uninstall removes it.

| Subcommand | Effect |
|---|---|
| `skills install` | Write the bundled skill to `~/.claude/skills/ghostchrome/` |
| `skills remove` | Remove the installed skill |
| `skills status` | Show whether it's installed and where |

Playwright CLI-compatible alias: `install --skills`.

### `config-print`

Print the resolved Playwright CLI-compatible config surface. `--config` loads a
JSON config file, and `.playwright/cli.config.json` is auto-loaded when present.
ghostchrome applies only fields it can actually honor today: CDP endpoint,
CDP headers, CDP timeout, headless, executable path,
Chromium launch args, proxy server/bypass/credentials, user data dir, navigation timeout,
`outputDir` for Playwright-compatible artifacts, `saveVideo` metadata auto-start,
`console.level`,
`browser.contextOptions.viewport`, `browser.contextOptions.userAgent`,
`browser.contextOptions.locale`, `browser.contextOptions.permissions`,
`browser.contextOptions.serviceWorkers`, `browser.contextOptions.storageState`,
and `browser.initScript`. Relative storage-state and init-script paths are
resolved from the config file directory. `cdpHeaders` are sent through Rod's
CDP websocket handshake, and `cdpTimeout` is treated as milliseconds for that
handshake. Supported permissions are granted with CDP
`Browser.grantPermissions`; unknown permission names are reported in
`unsupported_fields`. `serviceWorkers: "block"` bypasses existing service
workers via CDP and blocks future registrations with an init script. The
equivalent env override is `PLAYWRIGHT_MCP_BLOCK_SERVICE_WORKERS=true`.
`browser.initScript` registers JavaScript files with CDP before future page
scripts run; `browser.initPage` remains unsupported because it is a Playwright
TypeScript page setup surface, not a CDP/Rod primitive.
`browser.launchOptions.args` accepts Chromium switches in `--flag` or
`--flag=value` form; non-switch entries are reported in `unsupported_fields`.
`outputDir` changes the default directory for snapshot YAML files, default
trace output, and video metadata manifests; explicit command output paths still
win.
`console.level` sets the default filter for the `console` command and accepts
`error`, `warning`, `info`, or `debug`; `--level` and positional levels still
win.

Supported Playwright CLI environment overrides:

- `PLAYWRIGHT_CLI_SESSION`
- `PLAYWRIGHT_MCP_CONFIG`
- `PLAYWRIGHT_MCP_HEADLESS`
- `PLAYWRIGHT_MCP_ISOLATED=true`
- `PLAYWRIGHT_MCP_CDP_ENDPOINT`
- `PLAYWRIGHT_MCP_USER_DATA_DIR`
- `PLAYWRIGHT_MCP_EXECUTABLE_PATH`
- `PLAYWRIGHT_MCP_DEVICE`
- `PLAYWRIGHT_MCP_STORAGE_STATE`
- `PLAYWRIGHT_MCP_VIEWPORT_SIZE`
- `PLAYWRIGHT_MCP_USER_AGENT`
- `PLAYWRIGHT_MCP_IGNORE_HTTPS_ERRORS`
- `PLAYWRIGHT_MCP_TIMEOUT_NAVIGATION`
- `PLAYWRIGHT_MCP_CONSOLE_LEVEL`
- `PLAYWRIGHT_MCP_PROXY_SERVER`
- `PLAYWRIGHT_MCP_PROXY_BYPASS`
- `PLAYWRIGHT_MCP_OUTPUT_DIR`
- `PLAYWRIGHT_MCP_NO_SANDBOX`
- `PLAYWRIGHT_MCP_GRANT_PERMISSIONS`
- `PLAYWRIGHT_MCP_BLOCK_SERVICE_WORKERS`
- `PLAYWRIGHT_MCP_INIT_SCRIPT`
- `PLAYWRIGHT_MCP_SAVE_VIDEO`

A session resolves to `--connect` internally; `--user-profile`/`--proxy`/
`--stealth` are applied when the session's Chrome is first spawned.

### `serve`

Launch a long-lived Chrome and print its WebSocket URL. Other commands
attach with `--connect=auto` (or an explicit `--connect=ws://...`).
For most workflows `-s <name>` (above) is simpler — it spawns and reuses
the browser for you.

| Flag | Default | Effect |
|---|---|---|
| `--port N` | random | Chrome remote debugging port |

Idle shutdown defaults to 1 hour (`GHOSTCHROME_IDLE_TIMEOUT`). Set `0` to keep the daemon until the machine reboots. Attached / headed / leased Chrome is never reaped.

### `attach [session-name]`

Attach to an existing Chromium browser via CDP and register it as a session.
If `-s/--session` is omitted, the session name is `default`, and later commands
without `-s` reuse it while it is alive.

| Flag | Effect |
|---|---|
| positional `session-name` | Attach `default` or `-s` to an existing live ghostchrome session |
| `--cdp=chrome` | Discover a running Chrome CDP endpoint on local debug ports |
| `--cdp=msedge` | Discover a running Edge CDP endpoint on local debug ports |
| `--cdp=http://localhost:9222` | Resolve `/json/version` and attach |
| `--cdp=ws://...` | Attach directly to a browser WebSocket endpoint |
| `--endpoint` | Structured unsupported result for Playwright server endpoint mode |
| `--extension[=channel]` | Structured unsupported result for Playwright extension attach mode |

### `mcp`

Run ghostchrome as a Model Context Protocol server (stdio). Exposes 11
tools to LLM agents — see [`docs/mcp.md`](mcp.md).

### `contexts`

Manage named isolated browser contexts (incognito) inside a connected
Chrome. Letting multiple agents share one Chrome via separate contexts
avoids spawning multiple Chrome processes.

| Subcommand | Effect |
|---|---|
| `list` | Show all contexts |
| `clear <name>` | Reset cookies/storage of a context |
| `close <name>` | Close a context |
| `delete <name>` | Remove context + its profile dir |
| `save <name>` | Persist storageState to disk |
| `load <name> <state.json>` | Restore from a previously saved state |

### `tabs`

| Subcommand | Effect |
|---|---|
| `list` (default) | Show open tabs (index, URL, title) |
| `new [url]` | Open + activate a new tab (blank if no URL) |
| `switch <index>` | Make a tab active |
| `close <index>` | Close a tab |

Playwright CLI-compatible aliases: `tab-list`, `tab-new`, `tab-select`,
`tab-close`.

### `viewport [width] [height] [url]`

Set the viewport dimensions with width/height arguments or `--device`.
Alias: `resize`.

### `cookies`

| Subcommand | Effect |
|---|---|
| `list [--domain] [--path]` | Print cookies, optionally filtered |
| `set <name=value>` | Set a cookie |
| `delete <name>` | Delete a cookie |
| `clear` | Clear all cookies |

Playwright CLI-compatible aliases: `cookie-list`, `cookie-get`,
`cookie-set <name> <value>`, `cookie-delete`, `cookie-clear`.

### `storage save --output <path>` / `storage load <path>`

Save/restore the full storage state (cookies + localStorage +
sessionStorage) as Playwright-compatible JSON.
Aliases: `state-save [filename]`, `state-load <filename>`.

### `localstorage-*` / `sessionstorage-*`

Current-page key/value storage commands compatible with Playwright CLI naming:

| Command | Effect |
|---|---|
| `localstorage-list` / `sessionstorage-list` | List all key-value pairs |
| `localstorage-get <key>` / `sessionstorage-get <key>` | Read one value |
| `localstorage-set <key> <value>` / `sessionstorage-set <key> <value>` | Set one value |
| `localstorage-delete <key>` / `sessionstorage-delete <key>` | Delete one key |
| `localstorage-clear` / `sessionstorage-clear` | Clear the storage area |

### `import-profile`

Copy cookies/storage from a real Chrome/Edge/Brave profile on the same
machine into a ghostchrome profile. Useful to bootstrap a session that
needs to be already logged in.

### `login [site|url]`

Open a real browser window for the user to log in to a known site, then
persist cookies/storage into the active profile. Bundled flows for
common sites (Google, GitHub, LinkedIn, ...).

### `emulate`

Emulate a device preset (`iphone-15`, `pixel-8`, etc.). Sets viewport,
user agent, touch capability.

### `geolocation`

Override geolocation. Useful for testing locale-gated content.

### `extensions`

Manage bundled Chrome extensions (uBlock Lite, ICDC, Force Background
Tab) under `~/.ghostchrome/extensions/`. Auto-loaded with
`--default-extensions`.

| Subcommand | Effect |
|---|---|
| `install` | Install the curated default set |
| `list` | List installed extensions |

### `ai <goal>`

LLM-assisted action: given a natural-language goal, ghostchrome calls
the configured AI provider to plan and execute a series of CLI commands.
Requires an `ANTHROPIC_API_KEY` or compatible env var.

---

## Automation and scraping

### `agent <jsonl>`

Run an agent recipe from a JSONL file: each line is a step
(`{"op":"navigate","url":"..."}`, `{"op":"click","ref":"@3"}`, ...).
Preferred for deterministic multi-step scrapes.

### `batch [script]`

Like `agent`, but YAML/JSON script with retries, error handling, and
parallel branches.

### `collect <url> [url2 ...]`

Observer stream: NDJSON of Network + Console + Page events as the page
loads. Use for live debugging or to feed an analytics pipeline.

| Flag | Default | Effect |
|---|---|---|
| `--duration NS` | 10s | How long to listen |
| `--filter-net TYPES` | — | Restrict net events (e.g. `xhr,fetch,document`) |
| `--filter-url REGEX` | — | URL regex |

### `capture [url]`

Passive network capture. Records matching requests as JSON/NDJSON and can
include response bodies.

### `record <url>`

Interactive recorder: lets a human drive a real browser; ghostchrome
emits the equivalent agent JSONL script the agent can replay later.

---

## Network capture and replay

### `network [url]`

Playwright CLI-compatible active observer for completed network requests. It
reports requests captured while the command is running, and appends them to a
bounded session buffer when using `-s` or `--connect`.

| Flag | Default | Effect |
|---|---|---|
| `--wait N` | `1` | Seconds to keep observing after optional navigation |
| `--max N` | `0` | Stop after N completed requests (`0` = unlimited) |
| `--filter REGEX` | — | Filter requests by URL |
| `--static` | off | Include images, CSS, fonts, and media |
| `--request-body` | off | Include request bodies |
| `--request-headers` | off | Include request headers |
| `--clear` | off | Clear the current command log and return no entries |

### `route <pattern>`

Playwright CLI-compatible persistent route. By default it starts a background
route worker for the current session/CDP target and returns. Use `route-list`
to inspect active routes and `unroute [pattern|id]` to stop them. A persistent
route requires `-s/--session`, `--connect`, or an attached `default` session.

| Flag | Default | Effect |
|---|---|---|
| `--status N` | `200` | HTTP response status |
| `--body TEXT` | — | Response body, or `@path` to read a file |
| `--content-type TYPE` | auto | Content-Type response header |
| `--header NAME:VALUE` | — | Additional response header, repeatable |
| `--wait N` | `0` | Foreground route for N seconds (`0` = persist in background) |
| `--remove-header NAMES` | — | Strip comma-separated request headers before continuing the matched request |

### `route-list` / `unroute [pattern|id]`

List active route workers, or remove one route by id/pattern. `unroute` with no
argument removes every registered route.

### `intercept`

Block or fulfill requests matching URL glob patterns at the network layer.
The command runs until interrupted, so use it from a dedicated terminal or
inside a batch flow.

| Flag | Effect |
|---|---|
| `--block "*.png,*analytics*"` | Block matching requests |
| `--fulfill "*/api/users"` | Fulfill one pattern with `--body` |
| `--status 500` | Status code for `--fulfill` |
| `--content-type application/json` | Content-Type for `--fulfill` |
| `--rules rules.json` | Load multi-pattern rules from JSON |

### `network-state-set <online|offline>`

Playwright CLI-compatible command that toggles CDP network condition
emulation for the active page.

### `sniff-api <url>`

Listen to all JSON/API requests a page issues and emit them in a compact
catalogue. Pairs with `fetchapi` for headless re-querying.

### `http-replay <url>`

Replay a captured HAR or NDJSON trace against a page (record once,
deflake forever).

### `tracing-start` / `tracing-stop`

Playwright CLI-compatible command names for browser tracing. Requires a
persistent session or `--connect`. `tracing-stop` defaults to
`.playwright-cli/trace.zip`, matching Playwright CLI's path shape. The archive
contains CDP events plus metadata and is marked not Playwright Trace
Viewer-compatible until ghostchrome emits the real Playwright trace schema.
Use `--output trace.json` for raw CDP JSON.

- `tracing-start` — start Chrome CDP tracing
- `tracing-stop` — stop and save `.playwright-cli/trace.zip`
- `tracing-stop --output trace.json` — stop and save raw CDP JSON

### `video-start` / `video-chapter` / `video-stop`

Playwright CLI-compatible command names for video metadata and chapters.
Requires a persistent session or `--connect`. WebM frame recording is not
implemented yet; `video-stop` writes a manifest instead.

- `video-start [filename] --size=800x600` — start metadata capture
- `video-chapter "Title" --description="..." --duration=2000` — add a chapter
- `video-stop` — write `.playwright-cli/<name>.video.json`

Automatic recording metadata:

- `--config config.json` with `{ "saveVideo": { "width": 800, "height": 600 } }`
- `PLAYWRIGHT_MCP_SAVE_VIDEO=800x600`

These auto-start video metadata for persistent sessions and record the source
in the manifest. They do not create WebM frames yet; manifests keep
`webm_recorded=false`.

### `resume` / `step-over` / `pause-at <file:line>`

Playwright CLI-compatible command names for test debugging. They return a
structured unsupported result today because real execution requires a paused
Playwright `--debug=cli` session and the Playwright test debugging protocol,
not just a CDP browser connection.

### `trace-clear` / `trace-export` / `trace-replay`

Manage ghostchrome MCP session traces:

- `trace-clear` — reset a JSONL MCP trace file
- `trace-export` — render a self-contained HTML viewer
- `trace-replay` — print a chronological digest

### `algolia`

Inspect Algolia search backends (almost every e-commerce SPA uses one).
Sniffs the index name and surfaces a query-ready request.

---

## Global flags

Apply to every command (where meaningful):

| Flag | Default | Effect |
|---|---|---|
| `-s, --session NAME` | `$PLAYWRIGHT_CLI_SESSION`, then `$GHOSTCHROME_SESSION` | Auto-managed persistent session: spawn a Chrome (profile `NAME`) on first use, reuse it after. |
| `--connect URL` | — | Attach to existing Chrome (`auto` to discover on 127.0.0.1:9222-9229). |
| `--context NAME` | — | Use a named isolated context in the connected Chrome (parallel sessions, no extra Chrome). |
| `--headless` | true | Headless mode. Set `--headless=false` to show a window. |
| `--headed` | false | Playwright CLI-compatible inverse of `--headless`. |
| `--invisible` | false | macOS-only: collapse the window to invisible. |
| `--user-profile NAME` | — | Persist cookies/storage at `~/.ghostchrome/profiles/NAME/`. |
| `--config PATH` | — | Load mappable Playwright CLI JSON config fields. |
| `--stealth` | false | Bundled fingerprint patches + script blocker. |
| `--default-extensions` | false | Load bundled extensions on auto-launch. |
| `--proxy URL` | — | Route through proxy (basic auth via `http://user:pass@host:port`). |
| `--dismiss-cookies` | false | Auto-dismiss consent banners on every navigation. |
| `--timeout N` | 30 | Per-command timeout in seconds. |
| `--format text\|json` | text | Emit human text or structured JSON. |
| `--profile auto\|human\|agent` | auto | Output render profile; on `open`, local `--profile PATH` means browser user data dir. |

Environment variables:

| Var | Effect |
|---|---|
| `GHOSTCHROME_MCP_LAZY=1` | MCP server defers Chrome spawn until the first tool call. |
| `GHOSTCHROME_MCP_NO_BLOCKER=1` | Disable the auto-on script blocker even with `--stealth`. |
| `GHOSTCHROME_MCP_TRACE=path` | Persist a trace of every MCP tool call to `path` (debugging). |
| `GHOSTCHROME_BROWSER_PATH=/...` | Pin the Chromium binary explicitly. |
