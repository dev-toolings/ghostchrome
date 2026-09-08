# Architecture

This document describes the tree as it is on disk. Every path below exists;
every diagram is 7-bit ASCII, per the project rule.

The layout rule is one sentence: **`cmd/` holds thin mains, `internal/surface/`
translates a protocol into an operation, `internal/runtime` orchestrates the
operation, `internal/core/` owns the browser domain.** The op catalog in
`internal/ops` is the single source of truth for the operation names, their
arguments and which surfaces expose them; three registration tables and the
SDK contract are generated from it.

## System overview

```text
  +--------------------------+      +--------------------------------+
  | cmd/ghostchrome          |      | cmd/ghostchrome-mcp            |
  | main -> cli.Execute()    |      | main -> mcp.NewServer()        |
  | 25 lines                 |      | env-driven, GHOSTCHROME_*      |
  +-------------+------------+      +---------------+----------------+
                |                                   |
                v                                   v
  +-------------+-----------------------------------+-----------------+
  |  internal/surface        PROTOCOL ADAPTERS                        |
  |                                                                   |
  |  cli/          cobra commands, one file per command               |
  |    agent.go    JSONL stdio loop, the SDK transport                |
  |  mcp/          JSON-RPC over stdio, 19 tools                      |
  |  ai/           LLM tool_use loop, Anthropic + OpenAI providers    |
  +-------------+-----------------+---------------------+-------------+
                |                 |                     |
                |                 v                     |
                |   +-------------+-----------------+   |
                |   | internal/runtime              |   |
                |   | Session, bind, handlers_gen   |   |
                |   | one handler per op            |   |
                |   +-------------+-----------------+   |
                |                 |                     |
                v                 v                     v
  +-------------+-----------------+---------------------+-------------+
  |  internal/core           BROWSER DOMAIN                           |
  |                                                                   |
  |  engine/     browser, navigate, extract, interact, network,       |
  |              stealth, sessions, HTTP fast path      (54 files)    |
  |  antibot/    blocker patterns + consent banners                   |
  |  artifact/   HAR, trace, recorder                                 |
  |  dashboard/  WebSocket screencast + embedded UI                   |
  |  feedback/   observation stream, recovery hooks                   |
  |  inspect/    listing collector, React fiber walk                  |
  |  interact/   clipboard, raw mouse                                 |
  |  media/      pixel diff, screen recording                         |
  |  overlay/    numbered annotation, highlight                       |
  |  pagesetup/  permissions, prewarm, service workers                |
  |  policy/     domain allow/block, action gates                     |
  |  provider/   Chrome provisioning interface (local impl)           |
  |  proxy/      proxy auth, rotating pool                            |
  |  sites/      generic API sniffer + replayer                       |
  |  storage/    cookies decrypt/inject, storage snapshots            |
  |  vault/      AES-256-GCM + Argon2id encrypted state               |
  +--------+--------------------------------------+-------------------+
           |                                      |
           | net/http                             | CDP
           v                                      v
  +--------+--------------+          +------------+--------------+
  | Target server         |          | Chrome                    |
  | HTML, REST, Algolia   |          | launched, attached, or    |
  | SSR data islands      |          | reused via connect=auto   |
  +-----------------------+          +---------------------------+
```

Two support packages sit beside that stack: `internal/setup` (installation
state, transports, doctor, embedded skill) and `internal/compat/playwright`
(the Playwright CLI config contract). Both are reached from `internal/surface/cli`.

### Where the dependency rule holds and where it does not

`internal/core/engine` imports only `internal/core/media` and
`internal/core/policy`: the domain does not know about any surface. In the
other direction the rule is not yet absolute. `internal/surface/cli`,
`internal/surface/mcp` and `internal/surface/ai` all import
`internal/runtime` **and** `internal/core/*` directly, so a surface can still
reach past the orchestrator. That is the remaining gap between this tree and
the target described in `docs/architecture-audit.md`, and it is stated here rather
than drawn as if it were already closed.

## The catalog generates the surfaces

```text
                     +--------------------------+
                     | internal/ops/ops.go      |
                     | Catalog(): 33 ops        |
                     | name, args, surfaces     |
                     +------------+-------------+
                                  |
                                  v
                     +------------+-------------+
                     | internal/ops/gen         |
                     | rendering, in memory     |
                     +------------+-------------+
                                  |
        +-------------+-----------+-----------+-------------+
        |             |                       |             |
        v             v                       v             v
  +-----+------+ +----+-------------+ +-------+---------+ +-+-------------+
  | contracts/ | | internal/runtime | | internal/       | | internal/     |
  | commands   | | handlers_gen.go  | | surface/mcp/    | | surface/ai/   |
  | .json      | | 25 jsonl ops     | | tools_gen.go    | | tools_gen.go  |
  |            | |                  | | 19 mcp tools    | | 14 ai tools   |
  +-----+------+ +------------------+ +-----------------+ +---------------+
        |
        v
  +-----+-----------------------------+
  | sdk/typescript and sdk/python     |
  | coverage tests read the contract  |
  +-----------------------------------+
```

Regenerate with `go generate ./internal/ops/...` or `just contract`. The
generator refuses to write anything if `ops.Validate()` rejects the catalog,
and `internal/ops/gen/gen_test.go` re-renders in memory to prove the committed
files are current. A parity test in `internal/ops` fails when the JSONL, MCP
and AI surfaces drift from the catalog.

## Data flow

MCP host to Chrome:

```text
  +----------+   MCP     +----------------+   CDP    +-----------+
  | LLM      | +-------> | ghostchrome    | +------> | Chrome    |
  | agent    | <-------+ | -mcp           | <------+ | headless  |
  +----------+   stdio   +-------+--------+  events  +-----------+
                                 |
                                 v
                 +---------------+------------------+
                 | surface/mcp -> runtime -> core   |
                 | snapshot: navigate + extract     |
                 |           + errors in one call   |
                 | core/engine/extractor.go         |
                 |   a11y tree -> @1 @2 refs        |
                 +----------------------------------+
```

Shell to target server, without Chrome:

```text
  +----------+   args    +----------------+   HTTP   +---------------+
  | shell    | +-------> | ghostchrome    | +------> | target server |
  | script   | <-------+ | fastfetch      | <------+ | SSR or API    |
  +----------+   JSON    +-------+--------+   HTML   +---------------+
                                 |
                                 v
                 +---------------+------------------+
                 | core/engine/fastfetch.go         |
                 | core/engine/ssr_extract.go       |
                 | core/engine/rsc_extract.go       |
                 | core/antibot: block signals      |
                 +----------------------------------+
```

Legend:

```text
  +-----+       a package or process boundary
  +---->        a synchronous call, direction of the request
  <----+        the response
  v             control passing down a layer
  ^             control passing back up a layer
```

## Module map

### Binaries and repo root

| Path | Responsibility |
|---|---|
| `cmd/ghostchrome/main.go` | Stamps the version, wires the embedded skill, calls `cli.Execute()`. |
| `cmd/ghostchrome-mcp/main.go` | Standalone MCP server, configured only through `GHOSTCHROME_*`. |
| `skillbundle.go` | Root package owning the `//go:embed` of the agent skill. |
| `contracts/commands.json` | Generated op contract both SDKs are typed against. |

### internal/ops, internal/runtime

| Path | Responsibility |
|---|---|
| `internal/ops/ops.go` | The catalog: 33 ops with args, summaries and surface exposure. Carries the `//go:generate` directive. |
| `internal/ops/validate.go` | Catalog invariants the generator refuses to bypass. |
| `internal/ops/gen/gen.go` | Renders the contract and the three registration tables. |
| `internal/ops/cmd/gen/main.go` | `//go:build ignore` writer invoked by `go generate`. |
| `internal/runtime/session.go` | `Session`, the single browser-backed op executor; `Ops()` and `Handles()`. |
| `internal/runtime/bind.go` | Argument binding from the wire form to typed handler input. |
| `internal/runtime/handlers_gen.go` | Generated dispatch table, one entry per JSONL op. |

### internal/surface

| Path | Responsibility |
|---|---|
| `internal/surface/cli/` | 91 source files: one cobra command per file, plus `root.go` (groups, help) and `agent.go` (the JSONL stdio loop the SDKs speak). |
| `internal/surface/mcp/server.go` | MCP server lifecycle, prewarm, idle reap, `ExtraToolRegistrars` hook for recipes. |
| `internal/surface/mcp/tools.go` | Hand-written tool bodies; `tools_gen.go` holds the generated registrations. |
| `internal/surface/mcp/trace.go` | Tool-call tracing. |
| `internal/surface/ai/loop.go` | The autonomous agent loop: observe, choose a tool, act, repeat. |
| `internal/surface/ai/anthropic.go`, `openai.go`, `fake.go` | Provider clients and the hermetic test double. |
| `internal/surface/ai/tools.go` | Tool bodies; `tools_gen.go` holds the generated specs. |

### internal/core

| Path | Responsibility |
|---|---|
| `internal/core/engine/browser.go` | Rod lifecycle: launch, attach, `connect=auto`, provider hook. |
| `internal/core/engine/navigator.go`, `nav_wait.go`, `wait.go` | Navigation and the wait strategies (`domcontentloaded`, `load`, `stable`, `idle`). |
| `internal/core/engine/extractor.go`, `extractor_domfallback.go` | Accessibility tree to filtered DOM with `@N` refs, three levels, DOM fallback. |
| `internal/core/engine/ssr_extract.go`, `rsc_extract.go` | SSR data-island parser and the Next.js App Router RSC stream decoder. |
| `internal/core/engine/fastfetch.go`, `fastfetch_tls.go`, `fetchapi.go`, `httpclient.go` | The HTTP fast path: single-shot GET, TLS fingerprint, JSON-API requests, shared client. |
| `internal/core/engine/interactor.go`, `drag.go`, `drop.go`, `touch.go`, `human.go` | Click, type, hover, press, select, drag and drop, touch gestures, humanised input. |
| `internal/core/engine/locator.go`, `locator_wait.go` | Resolve elements by text, role or CSS, with Playwright-like auto-wait. |
| `internal/core/engine/observer.go`, `persistent_observer*.go`, `network_tracker.go`, `event_hub.go` | CDP multiplexing of Network, Console and Runtime into an observation stream. |
| `internal/core/engine/capture.go`, `intercept.go`, `intercept_rules.go` | Network capture and request block/fulfil through the Fetch domain. |
| `internal/core/engine/stealth.go`, `evasion.go`, `page_profile.go` | Anti-detection patches, per-OS UA and viewport defaults, bot-challenge waiting. |
| `internal/core/engine/session_registry.go`, `session_state.go`, `session_spawn_*.go`, `profile*.go`, `context_registry.go` | The transparent daemon: named sessions, persistent profiles, isolated contexts. |
| `internal/core/engine/preview.go`, `errors.go`, `snapshot_diff.go`, `format.go` | The page health report, console and network errors, snapshot diffing, output formatting. |
| `internal/core/engine/discover.go` | CDP port scan on 9222-9229 for `--connect=auto`. |
| `internal/core/engine/initscript.go` | User init scripts from `~/.ghostchrome/init-scripts/*.js`. |
| `internal/core/engine/video_runtime.go`, `popup_watch.go`, `mutation.go` | Video capture runtime, popup tracking, DOM mutation waits. |
| `internal/core/antibot/` | Anti-bot signal detection, script blocker patterns, cookie banner dismissal. |
| `internal/core/artifact/` | HAR 1.2 export, trace recording and replay. |
| `internal/core/dashboard/` | Live viewport stream over WebSocket with an embedded UI. |
| `internal/core/feedback/` | The observation stream and the recovery hook chain (stale ref, dialog, bot challenge, network settle). |
| `internal/core/inspect/` | Listing auto-detection and the React fiber walk. |
| `internal/core/interact/` | Clipboard read/write and raw mouse control. |
| `internal/core/media/` | Pixel-diff screenshot comparison and screen recording. |
| `internal/core/overlay/` | Numbered borders on interactive elements, persistent highlight. |
| `internal/core/pagesetup/` | Permission grants, Chrome prewarm, service worker handling. |
| `internal/core/policy/` | Domain allow/blocklist and the eval, upload and clipboard gates. |
| `internal/core/provider/` | Chrome provisioning interface with the local implementation. |
| `internal/core/proxy/` | Proxy authentication and the rotating proxy pool. |
| `internal/core/sites/` | Generic API traffic sniffer and replayer, no browser needed. |
| `internal/core/storage/` | Cookie decrypt and inject, localStorage and cookie snapshots. |
| `internal/core/vault/` | AES-256-GCM state vault with an Argon2id key derivation. |
| `internal/core/coretest/` | Browser scaffolding shared by the core test suites. |

### Support and out-of-tree

| Path | Responsibility |
|---|---|
| `internal/setup/` | Installation state, CLI/MCP transport selection, global instructions, `doctor`, embedded skill copy. |
| `internal/compat/playwright/` | The Playwright CLI config schema and its value rules. |
| `sdk/typescript/`, `sdk/python/` | Thin clients that spawn `ghostchrome agent` and speak the JSONL protocol. |
| `sdk/npm/` | Six versioned npm distribution manifests: the `@ghostchrome/cli` meta package plus five per-platform packages. Not build output. |
| `recipes/<site>/` | Private site scrapers, gitignored, compiled in only with `go build -tags recipes`. Their cobra entry points are `internal/surface/cli/<site>.go`, also gitignored. |
| `tools/benchmark/` | Fixtures, runner and recorded results. |
| `sdk/examples/` | Runnable end-to-end TypeScript and Python examples. |

## Execution modes

| Mode | Trigger | Chrome | Use case |
|---|---|---|---|
| HTTP only | `fastfetch`, `fetchapi` | none | SSR pages, REST and Algolia APIs |
| HTTP then Chrome | `--fallback-browser` | 0 or 1 | Try fast, fall back when blocked |
| Daemon (default) | any command, no flag | reused | Implicit "default" session, persistent across invocations |
| Explicit attach | `--connect=auto`, `--connect=ws://...` | reused | Share one Chrome between agents |
| Cold spawn | `GHOSTCHROME_NO_DAEMON=1` | spawned | Documented escape hatch only |
| MCP server | `ghostchrome mcp`, `ghostchrome-mcp` | reused or spawned | 19 stdio tools for an LLM host |
| JSONL agent | `ghostchrome agent` | reused or spawned | The SDK transport, 25 ops over stdio |
| AI loop | `ghostchrome ai` | reused or spawned | Autonomous LLM-driven browsing, 14 tools |
| Live dashboard | `ghostchrome dashboard` | reused or spawned | Real-time viewport stream |

## CLI command groups

`internal/surface/cli/root.go` assigns every command to one of these groups,
and `ghostchrome --help` prints them in this order.

| Group | Commands |
|---|---|
| Session | `serve` `login` `import-profile` `extensions` |
| Navigate | `navigate` `back` `forward` `scroll` `scroll-by` `scroll-to` `viewport` `emulate` `geolocation` `tabs` |
| Observe | `extract` `preview` `eval` `errors` `capture` `screenshot` `pdf` `perf` `collect` `watch` `react` |
| Interact | `click` `type` `press` `hover` `select` `fill-form` `upload` `dialog` `drag` `clipboard` `mouse` |
| Wait | `waitfor` `wait-port` `wait-url` |
| State | `cookies` `storage` `intercept` `assert` `batch` `trace-replay` `trace-export` `trace-clear` |
| Utility | `doctor` `init-script` `dashboard` |
| Recipes | `linkedin` `leboncoin` `autoscout24` `cars-listings` (only with `-tags recipes`) |

Commands with no group entry, such as `mcp`, `agent`, `ai` and `setup`, appear
under the default cobra listing.

## Op surface

33 ops in the catalog. The three surfaces do not expose the same subset, and
the divergences are deliberate; `internal/ops/ops.go` documents each one.

| Surface | Count | Ops |
|---|---|---|
| `jsonl` | 25 | `back` `check` `click` `close` `dblclick` `dialog` `errors` `eval` `extract` `fill` `forward` `hover` `init` `navigate` `press` `reload` `screenshot` `scroll_by` `scroll_to` `select` `tabs` `type` `uncheck` `url` `wait` |
| `mcp` | 19 | `back` `click` `dialog` `drag` `emulate` `eval` `fill_form` `forward` `hover` `navigate` `press` `screenshot` `select` `snapshot` `swipe` `tabs` `type` `upload` `wait_for` |
| `ai` | 14 | `click` `done` `errors` `eval` `extract` `hover` `navigate` `press` `scroll_by` `scroll_to` `select` `type` `url` `wait` |

Notable divergences: `snapshot` is MCP-only because it bundles navigate,
extract and errors into one call; `done` is AI-only and has no CDP side effect;
`wait_for` is the MCP spelling of the JSONL `wait`; `fill`, `init` and `close`
are JSONL-only lifecycle ops.

## Security layers

```text
  a CLI or MCP request
        |
        v
  +-----+--------------------------------------------------+
  | internal/core/policy                                    |
  |   AllowURL()      glob match on *.example.com           |
  |   AllowAction()   gate eval, upload, clipboard          |
  |   MaxNavigations  rate limit                            |
  +-----+--------------------------------------------------+
        |
        v
  +-----+--------------------------------------------------+
  | internal/core/vault                                     |
  |   Argon2id        32-byte key from a password           |
  |   AES-256-GCM     salt(16) + nonce(12) + ciphertext     |
  +-----+--------------------------------------------------+
        |
        v
  +-----+--------------------------------------------------+
  | internal/core/engine/stealth.go, evasion.go             |
  | internal/core/antibot                                   |
  |   navigator patches   webdriver, plugins, languages     |
  |   script blocker      DataDome, PerimeterX, reCAPTCHA   |
  +-----+--------------------------------------------------+
        |
        v
  Chrome, headless or headful
```

## Build and check

```text
  just build      +--> CGO_ENABLED=0 go build ./...
  just test       +--> go test -short ./...
  just contract   +--> go generate ./internal/ops/...
  just sdk-ts     +--> bunx tsc --noEmit and bun test
  just sdk-py     +--> python3 -m unittest discover -s tests
  just test-all   +--> test, sdk-ts, sdk-py
  just e2e        +--> both SDK examples against a live Chrome
```

The recipes build is separate and needs the tag:
`go build -tags recipes ./...`.
