# Architecture

## System overview

```
╔════════════════════════════════════════════════════════════════════════════════╗
║                             ghostchrome v2.0                                 ║
╠════════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  ┌────────────────────────────────┐   ┌────────────────────────────────────┐ ║
║  │    ghostchrome  (CLI binary)   │   │   ghostchrome-mcp  (MCP binary)   │ ║
║  │    main.go → cmd/ (cobra)      │   │   cmd/ghostchrome-mcp/main.go     │ ║
║  │                                │   │   env-driven · stdio JSON-RPC     │ ║
║  │  session   navigate   observe  │   │   MCP 2025-11-25 · 19 tools      │ ║
║  │  interact  wait       state    │   └──────────────────┬─────────────────┘ ║
║  │  util      recipes (tag)       │                      │                   ║
║  └───────────────┬────────────────┘                      │                   ║
║                  │                                       │                   ║
║  ╔═══════════════▼═══════════════════════════════════════▼════════════════╗  ║
║  ║                         engine/  (shared library)                      ║  ║
║  ║                                                                        ║  ║
║  ║  ┌── HTTP fast path ────────┐  ┌── Chrome path (Rod + CDP) ────────┐  ║  ║
║  ║  │ fastfetch    fetchapi    │  │ browser     navigator   extractor │  ║  ║
║  ║  │ ssr_extract  rsc_extract │  │ interactor  drag        clipboard │  ║  ║
║  ║  │ httpclient              │  │ locator     locator_wait  human   │  ║  ║
║  ║  │ antibot_blocker         │  │ observer    recovery    stealth   │  ║  ║
║  ║  └──────────┬───────────────┘  │ annotate   react       preview   │  ║  ║
║  ║             │                  │ capture    intercept   har       │  ║  ║
║  ║             │                  │ cookies    storage     emulate   │  ║  ║
║  ║             │                  │ initscript session_state         │  ║  ║
║  ║             │                  └─────────────────────┬────────────┘  ║  ║
║  ║             │                                        │               ║  ║
║  ║  ┌── Sub-packages ──────────────────────────────────────────────┐    ║  ║
║  ║  │                                                              │    ║  ║
║  ║  │  mcp/         17 MCP tools · shared browser + page           │    ║  ║
║  ║  │  ai/          LLM agent loop (Anthropic · OpenAI)            │    ║  ║
║  ║  │  policy/      domain allow/block · action gates              │    ║  ║
║  ║  │  vault/       AES-256-GCM + Argon2id encrypted state        │    ║  ║
║  ║  │  provider/    Chrome provisioning interface (Local)          │    ║  ║
║  ║  │  dashboard/   WebSocket screencast + embedded UI             │    ║  ║
║  ║  │  sites/       generic API sniffer + replayer                 │    ║  ║
║  ║  │                                                              │    ║  ║
║  ║  └──────────────────────────────────────────────────────────────┘    ║  ║
║  ╚════════════════════════════════════════════════════════════════════════╝  ║
║                                                                              ║
║           │ net/http                        │ CDP (DevTools Protocol)         ║
║           ▼                                 ▼                                ║
║  ┌─────────────────────────┐   ┌──────────────────────────────────────────┐  ║
║  │  Target server          │   │  Chrome                                 │  ║
║  │  HTML · REST · Algolia  │   │  local (Rod) · Provider interface       │  ║
║  │  SSR data islands       │   │  headless · invisible · connect=auto    │  ║
║  └─────────────────────────┘   └──────────────────────────────────────────┘  ║
║                                                                              ║
║  ┌── packages/  (//go:build recipes) ────────────────────────────────────┐   ║
║  │  linkedin · leboncoin · instagram · cars-listings                     │   ║
║  │  Site-specific scrapers — excluded from default binary                │   ║
║  └───────────────────────────────────────────────────────────────────────┘   ║
╚════════════════════════════════════════════════════════════════════════════════╝
```

## Data flow

```
┌──────────┐         ┌──────────────┐         ┌───────────────┐
│  LLM     │──MCP───▶│ ghostchrome  │──CDP───▶│    Chrome     │
│  Agent   │◀─stdio──│ -mcp         │◀─events─│  (headless)   │
└──────────┘         └──────┬───────┘         └───────┬───────┘
                            │                         │
                      engine/mcp/               engine/extractor
                      19 tools                  a11y tree → @refs
                            │                         │
                      engine/policy             engine/annotate
                      allow/block               numbered overlay
```

```
┌──────────┐         ┌──────────────┐         ┌───────────────┐
│  Shell   │──args──▶│ ghostchrome  │──HTTP──▶│ Target server │
│  Script  │◀─JSON───│ CLI          │◀─HTML───│ (SSR / API)   │
└──────────┘         └──────┬───────┘         └───────────────┘
                            │
                      engine/fastfetch
                      engine/ssr_extract
                      9 SSR sources
```

## Legend

```
══════  Package / group boundary
──────  Module boundary
──▶     Sync call / data flow
◀──     Response
```

---

## Module map

| Path | Responsibility |
|---|---|
| `main.go` | Entry point → `cmd.Execute()` |
| `cmd/` | One file per cobra command (~45 total). Thin glue, no business logic. |
| `cmd/ghostchrome-mcp/` | Standalone MCP binary, env-driven (`GHOSTCHROME_*`). |

### engine/ — HTTP fast path

| Path | Responsibility |
|---|---|
| `engine/fastfetch.go` | Single-shot HTTP GET with anti-bot detection. |
| `engine/fetchapi.go` | Generic JSON-API request + AlgoliaQuery preset. |
| `engine/ssr_extract.go` | Multi-framework SSR data island parser (9 sources). |
| `engine/rsc_extract.go` | Next.js App Router RSC stream decoder. |
| `engine/httpclient.go` | Shared HTTP client with realistic headers. |
| `engine/antibot_blocker.go` | Anti-bot signal detection + script blocker patterns. |

### engine/ — Chrome path

| Path | Responsibility |
|---|---|
| `engine/browser.go` | Rod browser lifecycle: launch / attach / connect=auto / ProviderFunc. |
| `engine/navigator.go` | Navigate + wait strategies (domcontentloaded/load/stable/idle). |
| `engine/extractor.go` | Accessibility-tree → filtered DOM with `@N` refs (3 levels). |
| `engine/interactor.go` | Click / type / hover / press / select on refs. |
| `engine/drag.go` | Drag & drop via mouse events (ref-based or coordinate-based). |
| `engine/clipboard.go` | CDP clipboard read/write with permission grant. |
| `engine/annotate.go` | Overlay numbered borders on interactive elements (bitmap font). |
| `engine/react.go` | React fiber tree walk + Suspense boundary detection. |
| `engine/locator.go` | Resolve elements by text/role/css. |
| `engine/locator_wait.go` | Playwright-like auto-wait states. |
| `engine/observer.go` | CDP multiplexer (Network + Console + Runtime) → NDJSON. |
| `engine/recovery.go` | Pluggable recovery hooks (stale-ref, dialog, bot-challenge). |
| `engine/stealth.go` | Anti-detection JS patches (navigator, webdriver, plugins). |
| `engine/human.go` | Bezier mouse paths, jittered key delays. |
| `engine/capture.go` | Streaming network capture with optional body fetch. |
| `engine/intercept.go` | Block / fulfill requests via Fetch domain. |
| `engine/har.go` | HAR 1.2 export. |
| `engine/preview.go` | All-in-one page health report. |
| `engine/initscript.go` | User init scripts (`~/.ghostchrome/init-scripts/*.js`). |
| `engine/cookies.go` | Cookie import/export/decrypt. |
| `engine/storage.go` | localStorage + cookie save/restore. |
| `engine/emulate.go` | Device / UA / color-scheme / timezone override. |
| `engine/touch.go` | Touch emulation + `Input.dispatchTouchEvent` swipe / tap. |
| `engine/imagediff.go` | Pixel-diff screenshot comparison. |
| `engine/page_profile.go` | Per-OS UA + viewport defaults. |
| `engine/discover.go` | CDP port scan (9222-9229) for `--connect=auto`. |

### engine/ — Sub-packages

| Path | Responsibility |
|---|---|
| `engine/mcp/` | MCP server: 19 tools, shared browser+page, snapshot management. |
| `engine/ai/` | LLM-driven browser agent loop (Anthropic + OpenAI providers). |
| `engine/policy/` | Action policy: domain allow/blocklist, eval/upload/clipboard gates. |
| `engine/vault/` | AES-256-GCM + Argon2id encrypted state vault. |
| `engine/provider/` | Chrome provisioning interface (Local impl; future: cloud). |
| `engine/dashboard/` | Live dashboard: WebSocket screencast + embedded static UI. |
| `engine/sites/` | Generic API traffic sniffer + replay without browser. |

### packages/ (build tag: recipes)

| Path | Responsibility |
|---|---|
| `packages/linkedin/` | LinkedIn People search + Content search → CSV. |
| `packages/leboncoin/` | Leboncoin scraper. |
| `packages/instagram/` | Instagram scraper. |
| `packages/cars-listings/` | Cars listings aggregator (autoscout24, etc.). |

---

## Execution modes

| Mode | Trigger | Chrome | Use case |
|---|---|---|---|
| HTTP only | `fastfetch` / `fetchapi` | none | SSR pages, REST/Algolia APIs |
| HTTP → Chrome | `--fallback-browser` | 0-1 | Try fast, fall back when blocked |
| Chrome attach | `--connect=auto` / `--connect=ws://…` | reuse | No spawn cost, persistent state |
| Chrome launch | default (no `--connect`) | spawn | Full automation, ephemeral profile |
| MCP server | `ghostchrome mcp` or `ghostchrome-mcp` | reuse/spawn | LLM agent integration (19 tools) |
| Live dashboard | `ghostchrome dashboard` | reuse/spawn | Real-time viewport stream + activity |
| Agent loop | `ghostchrome agent` | reuse/spawn | LLM-driven JSONL with observation |
| Observation | `--observe` flag | reuse/spawn | Debug: capture XHR/console/errors |

---

## CLI command groups

| Group | Commands |
|---|---|
| **Session** | `serve` `login` `import-profile` `extensions` |
| **Navigate** | `navigate` `back` `forward` `scroll` `scroll-by` `scroll-to` `viewport` `emulate` `geolocation` `tabs` |
| **Observe** | `extract` `preview` `eval` `errors` `capture` `screenshot` `pdf` `perf` `collect` `watch` `react` |
| **Interact** | `click` `type` `press` `hover` `select` `fill-form` `upload` `dialog` `drag` `clipboard` `mouse` |
| **Wait** | `waitfor` `wait-port` `wait-url` |
| **State** | `cookies` `storage` `intercept` `assert` `batch` `trace-replay` `trace-export` `trace-clear` |
| **Utility** | `doctor` `init-script` `dashboard` `mcp` |
| **Recipes** | `linkedin` `leboncoin` `autoscout24` `cars-listings` |

---

## MCP tool surface (19 tools)

| Tool | Action |
|---|---|
| `snapshot` | Page status + errors + network + DOM with `@refs` |
| `navigate` | Go to URL, return status |
| `click` | Click `@ref` |
| `type` | Type text into `@ref` (with optional submit) |
| `select` | Pick option(s) in `<select>` |
| `press` | Send keyboard key |
| `hover` | Hover `@ref` |
| `drag` | Drag from `@ref` to `@ref` (mouse events) |
| `swipe` | Single-finger touch gesture between two viewport points |
| `emulate` | Device profile: viewport, DPR, mobile, touch, UA, color-scheme |
| `fill_form` | Bulk fill `{@ref: value}` from JSON |
| `upload` | Attach files to `<input type=file>` |
| `tabs` | List / switch / close tabs |
| `dialog` | Accept or dismiss JavaScript dialogs |
| `wait_for` | Wait for selector / text / timeout |
| `eval` | Run JS (escape hatch) |
| `screenshot` | Capture viewport/page/element (with annotate) |
| `back` | Browser history back |
| `forward` | Browser history forward |

---

## Security layers

```
┌──────────────────────────────────────────────────────────────┐
│                      Request flow                            │
│                                                              │
│  CLI / MCP                                                   │
│    │                                                         │
│    ├──▶ engine/policy/     domain allow/block                │
│    │    AllowURL()          *.example.com glob matching       │
│    │    AllowAction()       gate eval · upload · clipboard    │
│    │    MaxNavigations      rate limit                        │
│    │                                                         │
│    ├──▶ engine/vault/      encrypted state                   │
│    │    Argon2id KDF        32-byte key from password         │
│    │    AES-256-GCM         salt(16) ‖ nonce(12) ‖ ciphertext │
│    │                                                         │
│    ├──▶ engine/stealth/    anti-detection                    │
│    │    navigator patches   webdriver · plugins · languages   │
│    │    script blocker      DataDome · PerimeterX · reCAPTCHA │
│    │                                                         │
│    └──▶ Chrome              headless or invisible             │
└──────────────────────────────────────────────────────────────┘
```
