# Chrome path

When fast-path can't deliver — JS rendering, interaction, anti-bot challenges that need a real browser — ghostchrome drives Chrome via CDP (Rod). All the LLM-friendly affordances live here: filtered a11y tree with `@N` refs, locator auto-wait, observer NDJSON, agent JSONL loop, recovery hooks.

## Three ways to get a Chrome

| Mode | Flag | Behavior |
|---|---|---|
| **Attach to running Chrome** (preferred) | `--connect=auto` | Scan ports 9222-9229, pick lowest. Zero spawn cost. |
| Attach to specific endpoint | `--connect=ws://...` | Use the WebSocket URL given. |
| Spawn fresh headless | `--launch` (alias) or no `--connect` | Ephemeral profile. Default. |
| Headful off-screen | `--invisible` | Real GPU, no visible window. Best for anti-bot. |

The runtime policy in `CLAUDE.md` says: prefer attach. Spawn is a fallback. With `--connect=auto`, ghostchrome doesn't mutate the user's tab UA/viewport unless `BrowserOpts.ApplyProfile` is explicitly set.

## Refs and locators

Every command that touches an element accepts either:

- **A ref** (`@1`, `@2`, …) from the latest snapshot. Snapshots come from `preview`, `extract`, `navigate --extract`, or in-flow nav.
- **A locator** via `--by-role`, `--by-name`, `--by-label`, `--by-text` (substring, case-insensitive on the a11y name).

```bash
ghostchrome click @3 https://app.com/page
ghostchrome click --by-role button --by-name "Submit" https://app.com/page
ghostchrome type --by-label "Email" "user@test.com" https://app.com/page
```

### Auto-wait (Playwright-like)

Add `--wait-for=<state>` and `--wait-timeout-ms=N` to any interaction command:

| State | Polling target |
|---|---|
| `attached` | element exists in DOM |
| `visible` | element exists AND has bounding-box visibility |
| `hidden` | element absent OR display:none |
| `enabled` | element not disabled |
| `stable` | bounding-box unchanged for 100 ms |

Polling: 100 ms initial, exponential backoff up to 500 ms, deadline = timeout. Refs preserve the original `@N → backendNodeID` mapping (no silent re-extract).

```bash
ghostchrome click --by-role link --by-name "Next page" \
  --wait-for=visible --wait-timeout-ms=10000 \
  https://app.com/listing
```

## Observer (live CDP stream)

Two ways to consume CDP events:

### Dedicated `watch` command

```bash
ghostchrome watch [URL] \
  --duration 6s \
  --filter-net=doc,xhr,fetch \
  --filter-net-url 'pattern' \
  --filter-console=error,warning \
  --max-events 200 \
  --observe-out events.jsonl
```

NDJSON shape:

```json
{"ts":1777907000000, "kind":"net",     "method":"GET",  "url":"...", "status":200, "type":"Document", "size":61234, "duration_ms":42}
{"ts":1777907000123, "kind":"console", "level":"error", "text":"Uncaught TypeError…", "source":"https://...:42:11"}
{"ts":1777907000456, "kind":"error",   "level":"error", "text":"Unhandled promise rejection: …"}
{"ts":1777907000789, "kind":"page",    "event":"loadEventFired", "frame":"AB12CD…"}
```

### Sidecar flag

Add `--observe` to any command. Events captured during op execution are returned alongside the result:

```bash
ghostchrome --observe navigate https://app.com 2> events.jsonl
ghostchrome --observe --observe-out events.jsonl click @1 https://app.com
```

### What's wired

- `Network.requestWillBeSent` → start tracking (id → method, URL, type, tStart)
- `Network.responseReceived` → status, mime, type
- `Network.loadingFinished` → emit `net` with size + duration
- `Network.loadingFailed` → emit `net` with `failed` reason
- `Runtime.consoleAPICalled` → `console`
- `Runtime.exceptionThrown` → `error`
- `Page.frameNavigated` / `Page.lifecycleEvent` (load/DOMContentLoaded/networkIdle) / `Page.loadEventFired` → `page`

## Agent JSONL loop

Run an LLM-driven workflow without the LLM having to learn ghostchrome's flag grammar — just a JSONL protocol on stdin/stdout.

```bash
ghostchrome agent
```

Stdin:

```
{"id":"r1","op":"navigate","args":{"url":"https://example.com"}}
{"id":"r2","op":"extract","args":{"level":"skeleton"}}
{"id":"r3","op":"click","args":{"ref":"@1"}}
{"id":"r4","op":"close"}
```

Stdout (one JSON object per line):

```json
{"id":"r1","ok":true,"result":{...},"observation":{...}}
{"id":"r2","ok":true,"result":{"refs":{"@1":{...}},"nodes":[...]},"observation":{...}}
```

### Supported ops

`init` `navigate` `back` `forward` `extract` `click` `type` `press` `hover` `select` `fill` `scroll_by` `scroll_to` `eval` `screenshot` `wait` `errors` `url` `close`.

### Observation packet

Every op response includes an `observation` object describing what changed:

```json
{
  "url": "https://example.com/dashboard",
  "console_errors": [{"level":"error","text":"…","source":"…"}],
  "network_failed": [{"url":"…","status":404,"failed":"…"}],
  "a11y_diff": "added 3 nodes, removed 1, changed 2",
  "dialog": "Are you sure?",
  "captcha_hint": "DataDome challenge detected"
}
```

With `--observe`, `events:[]` from the observer are also embedded in the response.

### Recovery hooks

When an op fails with a known recoverable error, the loop runs the chain in order:

| Hook | Triggers on | Action |
|---|---|---|
| `RecoverStaleRef` | `ErrStaleRef` | returns informative error — caller must explicitly re-extract (ref-preserving by design) |
| `RecoverDialogAccept` | dialog detected via short probe | `dialog accept` then retry |
| `RecoverBotChallenge` | DataDome 403 / Cloudflare 503 | `WaitForBotChallenge(10s)` then retry |
| `RecoverNetworkSettle` | timeout-class errors | `wait stable 2s` then retry |

Use `agentSession.recoveryHooks` to swap or extend the chain.

## Persistent sessions

```bash
# Terminal 1 — long-lived Chrome (single profile, cookies persist)
ghostchrome serve --port 9222 --user-profile work

# Terminal 2 — operate on the same Chrome
ghostchrome --connect ws://127.0.0.1:9222 tabs               # list tabs
ghostchrome --connect ws://127.0.0.1:9222 --tab 0 \
  navigate https://app.com/login
ghostchrome --connect ws://127.0.0.1:9222 --tab 0 \
  type --by-label "Email" "user@x.com"
```

For sites that bind cookies to a TLS fingerprint (LinkedIn, Cloudflare-hard targets), use:

```bash
ghostchrome --user-profile linkedin login linkedin    # headful manual auth
# subsequent runs reuse the saved cookies + same Chrome binary
```

## Stealth / anti-detection

```bash
ghostchrome --stealth navigate https://protected.fr
```

Layered:

1. Launcher flags (`disable-blink-features=AutomationControlled`, no `enable-automation`)
2. CDP `Page.addScriptToEvaluateOnNewDocument` patches via `engine/stealth.go` (navigator.webdriver, plugins, languages, chrome.runtime)
3. Optional `--invisible` for real GPU/fonts (off-screen window, no visible UI)
4. Optional `--human` for Bezier mouse paths and jittered key delays

For DataDome / Cloudflare-hard pages: `--stealth` first; if still blocked, `--user-profile <name>` after a successful `login` to inherit the device cookie.

## Validation & assertions

```bash
ghostchrome assert text "Welcome"             https://app.com/dash
ghostchrome assert selector-visible "button.save" https://app.com
ghostchrome assert url "/dashboard"           # current URL after redirects
ghostchrome assert no-console-errors          https://app.com
ghostchrome assert no-network-4xx             https://app.com
```

Exit codes: 0 pass, 1 fail. Pipe-friendly.

## Performance

```bash
ghostchrome perf https://app.com --budget-lcp 2500 --budget-cls 0.1 --budget-tbt 200
```

Returns Web Vitals (LCP, CLS, TBT, FCP, TTI) and exits non-zero if any budget is exceeded.

## Capture (HAR)

```bash
ghostchrome capture https://app.com --har out.har --include-bodies --max-events 500
```

Streams a HAR 1.2 file as the page loads. Use with `--filter-net-url` to scope.
