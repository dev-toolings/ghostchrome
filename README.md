# ghostchrome

**Ultra-light browser automation CLI for LLM agents.** Single Go binary, native Chrome DevTools Protocol, compact direct output, persistent sessions, and no Node runtime. A modern Playwright alternative built for AI agents that drive a browser in a loop.

[![Go](https://img.shields.io/github/go-mod/go-version/dev-toolings/ghostchrome?logo=go)](go.mod)
[![Release](https://img.shields.io/github/v/release/dev-toolings/ghostchrome?label=release&logo=github)](https://github.com/dev-toolings/ghostchrome/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Benchmark: reproduced Aug 2026](https://img.shields.io/badge/benchmark-reproduced_Aug_2026-5b8def)](benchmark/results-real-2026-08-25.md)

```console
$ ghostchrome preview http://localhost:3000
[200] Dashboard — http://localhost:3000 (134ms)
[errors] none
[network] 12 reqs, 0 failed
[dom]
  h1 Dashboard
  @1 b Add user
  table 5 rows
  @2 a>/settings Settings
```

One command. ~50 ms warm. ~2,000 tokens. Refs (`@1`, `@2`) you can click and type into next.

---

## Table of contents

- [Why ghostchrome](#why-ghostchrome)
- [Benchmark](#benchmark)
- [Install](#install)
- [Quickstart](#quickstart)
- [How it works](#how-it-works)
- [Comparison with playwright-cli, Playwright, Puppeteer, chromedp](#comparison)
- [Using it with LLM agents](#using-it-with-llm-agents)
- [Command reference](#command-reference)
- [Playwright CLI parity](#playwright-cli-parity)
- [Status & roadmap](#status--roadmap)
- [Contributing](#contributing)
- [License](#license)

---

## Why ghostchrome

LLM-driven browser automation has a context-budget problem: browser schemas,
snapshots, and repeated command output compete with the code and reasoning an
agent actually needs. ghostchrome keeps the CLI path direct and compact, with a
filtered accessibility tree and a single static Go binary. The MCP surface is
deliberately small too, although its current structured snapshot is richer—and
larger—than Playwright MCP's file-backed YAML snapshot. The benchmark below
reports both results instead of collapsing unlike surfaces into one headline.

**Designed for AI agents** that drive a browser via Claude Code, the Anthropic Agent SDK, Aider, Cursor, OpenAI's Agents SDK, or any custom loop. Use it as a **Playwright alternative for headless Chrome web scraping**, as a **CDP CLI** for ops automation, or as the browsing tool behind a custom agent. No JSON-RPC overhead, no Node runtime, no `npm install`. Just `ghostchrome <command> <url>` and read the output.

What you get:
- **Filtered accessibility tree** — only interactive elements get refs (`@1`, `@2`), 3-5× fewer nodes than a full a11y dump.
- **Three extraction levels** — `skeleton` (minimal), `content` (text), `full` (everything named).
- **Transparent daemon** — every command auto-spawns a persistent background Chrome on first use (no `serve`, no `--connect`, zero config). Just run `ghostchrome goto <url>` and it works.
- **CDP-native** — built on [Rod](https://github.com/go-rod/rod), so iframe handling, stealth patches, and event capture work out of the box.
- **Single ~19 MB binary** — no Node.js, no `npm install`, no Playwright browsers download.
- **Three ways to drive it** — the CLI, an MCP server (19 tools, drop-in for `@playwright/mcp`), or typed Python / TypeScript SDKs over the persistent JSONL `agent` loop.

---

## Benchmark

The current reproducible comparison uses `@playwright/cli@0.1.18`,
`@playwright/mcp@0.0.79`, and the current ghostchrome checkout. Every pairing
runs the same Chromium 128 executable against five deterministic local pages.
Results are medians over seven warm-session trials after a discarded warm-up,
and every output is checked for expected page content.

Current Playwright CLI and MCP responses link to a YAML snapshot file. The
effective payload below includes that file because an agent must read it to use
the page state. No redundant `browser_snapshot` call is counted after
`browser_navigate`.

### Page inspection

| Surface | Metric | ghostchrome | Playwright | Result |
|---|---|---:|---:|---|
| CLI | Effective output, five-page total | 21,782 B | 41,034 B | ghostchrome **1.88× smaller** |
| CLI | Median-time sum | 298 ms | 3,377 ms | ghostchrome **11.3× faster** |
| MCP | Effective output, five-page total | 84,171 B | 41,015 B | ghostchrome **2.05× larger** |
| MCP | Median-time sum | 242 ms | 317 ms | ghostchrome **1.31× faster** |
| MCP | Serialized tool schemas | 9,000 B / 16 tools | 18,502 B / 24 tools | ghostchrome **2.06× smaller** |

All four inspection pairings found the expected content in **35/35** measured
runs. The estimated token totals (`ceil(bytes / 4)`) are 5,446 vs 10,259 for
CLI and 21,043 vs 10,254 for MCP.

### Verified interaction

The interaction task navigates to a product, locates the `Quantity` combobox
from the snapshot, selects `3`, evaluates `#qty.value`, and requires the result
to equal `3`.

| Surface | Tool | Success | Agent calls/reads | Effective output | Duration |
|---|---|---:|---:|---:|---:|
| CLI | ghostchrome | 7/7 | 3 | 3,468 B | 443 ms |
| CLI | Playwright | 7/7 | 4 | 6,515 B | 2,459 ms |
| MCP | ghostchrome | 7/7 | 3 | 11,770 B | 408 ms |
| MCP | Playwright | 7/7 | 4 | 6,542 B | 634 ms |

The honest takeaway: **ghostchrome CLI is both smaller and faster in this
agent loop**. ghostchrome MCP is faster and has much smaller tool schemas, but
its current snapshot payload is larger because it serializes both `dom.nodes`
and a separate `dom.refs` map. That duplication is now a measured optimization
target, not a hidden caveat.

Full protocol, per-site medians, limitations, and reproduction details:
[`benchmark/results-real-2026-08-25.md`](benchmark/results-real-2026-08-25.md).
The raw-sample helpers are [`benchmark/cli-measure.mjs`](benchmark/cli-measure.mjs)
and [`benchmark/mcp-measure.mjs`](benchmark/mcp-measure.mjs).

The agent-browser hot-path results and release gates are tracked in
[`benchmark/results-agent-browser-click.md`](benchmark/results-agent-browser-click.md)
and [`docs/reliability-plan.md`](docs/reliability-plan.md).

---

## Install

ghostchrome installs the same way [`@playwright/cli`](https://www.npmjs.com/package/@playwright/cli)
does — one command to get the binary, one command to wire it into your coding
agent — except there is **no Node runtime and no browser download**: it's a
single static Go binary.

| | playwright-cli | ghostchrome |
|---|---|---|
| Get the tool | `npm install -g @playwright/cli` | `curl \| bash` or `bun install -g @ghostchrome/cli` |
| Wire into the agent | `playwright-cli install --skills` | `ghostchrome install --skills` |
| Daemon | requires `open` before `goto` | transparent — just run any command |
| Uninstall | manual | `ghostchrome uninstall --purge --yes` |
| Runtime | Node.js + Playwright + FFmpeg (~330 MB) | one ~19 MB binary, system Chrome |

### 1. Install one runtime

```bash
bun install -g @ghostchrome/cli       # or: bunx @ghostchrome/cli <cmd>
# npm install -g @ghostchrome/cli     # works too
```

The package resolves the prebuilt Go CLI binary for your platform (Linux/macOS,
amd64/arm64; Windows amd64) — no Node runtime, no postinstall, no browser
download. For exclusive CLI/MCP setup and global Claude/Codex/Grok skill
installation, use the mode-aware installer below.
Prefer a single binary with no package manager? Use the installer:

```bash
# Install exactly one runtime (the mode is intentionally mandatory).
curl -fsSL https://raw.githubusercontent.com/dev-toolings/ghostchrome/main/scripts/install.sh \
  | bash -s -- --mode cli --clients claude,codex,grok

# Or install the standalone MCP runtime instead of the CLI.
curl -fsSL https://raw.githubusercontent.com/dev-toolings/ghostchrome/main/scripts/install.sh \
  | bash -s -- --mode mcp --clients claude,codex,grok
```

The installer writes the selected binary under `~/.ghostchrome/bin`, verifies
the release checksum and installs the shared skill for each selected client.
It refuses to overwrite the opposite runtime; switch explicitly with
`ghostchrome setup switch --to cli|mcp --yes`.

Verify the selected runtime:

```bash
# CLI mode
ghostchrome --version
ghostchrome setup doctor --strict

# MCP mode
ghostchrome-mcp --version
claude mcp list              # or the equivalent Codex/Grok client check
```

### 2. Wire it into your coding agent

In MCP mode, `install.sh --mode mcp` registers Claude, Codex, and Grok
automatically. The equivalent manual Claude registration is:

```bash
claude mcp add ghostchrome -- ~/.ghostchrome/bin/ghostchrome-mcp

# …or attach to an already-running Chrome instead of launching one
GHOSTCHROME_CONNECT=auto claude mcp add ghostchrome -- ~/.ghostchrome/bin/ghostchrome-mcp
```

For Codex, Cursor, Aider, or a custom loop see [Using it with LLM agents](#using-it-with-llm-agents).

### Other install methods

- **Prebuilt binaries** — macOS (Intel/ARM), Linux (amd64/arm64), Windows on the
  [Releases](https://github.com/dev-toolings/ghostchrome/releases) page (`ghostchrome` +
  `ghostchrome-mcp`, with `checksums.txt`).
- **From source** — `git clone https://github.com/dev-toolings/ghostchrome && cd ghostchrome && go build -o ghostchrome .`

> **Note:** `go install …@latest` is not supported on this repo. Versioning was
> reset to `v0.1.0`, but the earlier `v1.0.0` is pinned immutably in the Go module
> proxy, so `@latest` resolves to stale code. Use the installer, a prebuilt binary,
> or build from source.

### Requirements

- Chrome or Chromium installed. If none is found, [Rod](https://github.com/go-rod/rod) auto-downloads a compatible Chromium to `~/.cache/rod/` on first run.

---

## Quickstart

Every command auto-spawns a persistent background Chrome on first use — no `serve`, no `open`, no setup. Just run the command.

### See a page

```bash
ghostchrome preview https://example.com
```

Single command returns status code, page title, console + network errors, request count, and a compact DOM with refs. The first call auto-starts the daemon; subsequent calls reuse it (~15 ms overhead).

### Extract a clickable DOM

```bash
ghostchrome extract https://news.ycombinator.com --level content
```

Compact accessibility tree with refs (`@1`, `@2`, …). Three levels: `skeleton` (interactive only), `content` (adds text), `full` (everything named).

### Drive the page

```bash
# Each command can navigate first, then act, then return the new snapshot.
ghostchrome click @3 https://example.com/login
ghostchrome type  @1 "alice@example.com" https://example.com/login
ghostchrome press Enter https://example.com/login
```

Refs come from the previous snapshot. The browser session is preserved automatically via the implicit daemon (no `--connect` needed).

### Named sessions (`-s`, playwright-cli-style)

```bash
ghostchrome -s work goto https://example.com/login   # spawns a persistent Chrome on first use
ghostchrome -s work type  @1 "alice@example.com"      # reuses it — no ws:// to copy, state persists
ghostchrome -s work click @3
ghostchrome -s work extract --level content

ghostchrome sessions list           # work  :PORT  alive  pid …
ghostchrome sessions stop work      # tear it down
```

`-s <name>` (or `$PLAYWRIGHT_CLI_SESSION`, falling back to `$GHOSTCHROME_SESSION`) auto-launches a
persistent Chrome on first use, bound to a disk profile of the same name (cookies persist under
`~/.ghostchrome/profiles/<name>`), and reuses it — including the active tab — across calls.
Per-call latency drops to ~50 ms. No ws:// URL to manage. Manage sessions with
`ghostchrome sessions list | stop <name> | kill-all`.

Emulation is sticky per session. `ghostchrome viewport 390 844` or
`ghostchrome emulate --device iphone-14` keeps applying to every following command (CDP drops the
override when the process exits, so ghostchrome replays it on attach), which is what makes a
responsive audit across several commands trustworthy. Clear it with `ghostchrome emulate --reset`.

> Prefer to manage Chrome yourself? `ghostchrome serve --port 9222` prints a ws:// URL and any
> command can attach with `--connect=auto` (discovers a serve on 127.0.0.1:9222-9229). A Chrome you
> attached to yourself is never re-emulated on attach: keep such a flow in one process
> (`ghostchrome batch`).

### Debug a page

```bash
ghostchrome errors https://your-site.test --level all
```

Captures `Runtime.consoleAPICalled` + `Runtime.exceptionThrown` + `Log.entryAdded` (CORS, CSP, mixed content, network ERR_*) + every HTTP 4xx/5xx — all in one snapshot.

---

## How it works

```
your agent → ghostchrome CLI → Rod (Go) → Chrome DevTools Protocol → Chrome
```

1. **CDP Accessibility tree** is fetched and **filtered**: only nodes that are interactive (or named ancestors) are kept. Everything is compressed into one indented text format with `@N` refs.
2. **Three extraction levels** let an agent ask for exactly the granularity it needs. Most agent loops stay at `content`.
3. **Refs are stable within a snapshot** and replayed on the next command via element-state cache, so `click @3` works without a new selector.
4. **Output is text first** — no JSON wrapping unless you ask for `--json`. The agent reads what a human would read in DevTools.
5. **Transparent daemon** — auto-spawns a persistent background Chrome on first use. Named sessions (`-s work`, `-s research`) run parallel isolated browsers. No `serve` needed.

Architecture, CLI reference, MCP server, anti-bot, and fast-path docs live in [`docs/`](docs/README.md).

---

## Comparison

### Current measured comparison

The August 2026 benchmark separates CLI and MCP instead of publishing one
cross-surface score:

- **CLI inspection:** ghostchrome uses 1.88× fewer effective bytes and is 11.3×
  faster across the deterministic five-page set.
- **CLI interaction:** ghostchrome completes the verified select-and-evaluate
  task 5.55× faster with 1.88× fewer effective bytes.
- **MCP inspection:** ghostchrome is 1.31× faster and exposes 2.06× fewer tool
  schema bytes, but its snapshot payload is 2.05× larger.
- **MCP interaction:** ghostchrome is 1.55× faster, while Playwright uses 1.80×
  fewer effective payload bytes.

These are measured outcomes, not a universal operation score. Network-heavy
sites, different extraction levels, browser engines, model behavior, and cold
startup can change the result. See the [full benchmark report](benchmark/results-real-2026-08-25.md).

### Feature comparison

| | ghostchrome | playwright-cli | Playwright (raw) | Puppeteer | chromedp |
|---|---|---|---|---|---|
| Target | LLM agents | LLM agents | Devs / QA | Devs | Devs (Go) |
| Runtime | Static Go binary | Node.js | Node.js | Node.js | Go binary |
| Install | `curl \| sh` or `bun i -g` | `npm i -g @playwright/cli` | npm + browser DL | npm + browser DL | `go install` |
| Install size | ~19 MB | ~330 MB | ~330 MB | ~280 MB | ~20 MB |
| Daemon | transparent (auto) | requires `open` first | n/a | n/a | n/a |
| CLI inspection payload | **1×** | **1.88× larger** (measured) | n/a | n/a | n/a |
| MCP inspection payload | **2.05× larger** (current structured JSON) | **1×** (file-backed YAML) | n/a | n/a | n/a |
| Multi-browser | Chrome only | Chrome / FF / WebKit | Chrome / FF / WebKit | Chrome / FF | Chrome only |
| Refs for click/type | `@1`, `@2` | `e1`, `e2` | CSS / XPath | CSS / XPath | CSS / XPath |
| Stealth | built-in patches | none | external plugin | external plugin | manual |
| Snapshot caching | yes (by URL) | yes (in-process) | n/a | n/a | n/a |
| Uninstall | `ghostchrome uninstall` | manual | manual | manual | manual |

### When to pick what

- **ghostchrome** — you're piloting Chrome from an LLM agent and want a single
  binary, a zero-config daemon, compact CLI output, and low warm-call latency.
  For MCP, choose it for the smaller tool surface and speed—not because the
  current structured snapshot is smaller.
- **playwright-cli** — you need WebKit / Firefox, Playwright Trace Viewer, or `run-code` (arbitrary Playwright API execution).
- **Playwright (raw)** — you're writing E2E test suites, not driving an agent.

### Parity with playwright-cli

ghostchrome covers the **agent-relevant verb surface** of
[`@playwright/cli`](https://www.npmjs.com/package/@playwright/cli) —
`open/goto`, `click`, `dblclick`, `type`/`fill`, `check`/`uncheck`,
`select`, `hover`, `drag`, `press`, `upload`, `snapshot`/`extract`, `eval`,
`reload`, `back`/`forward`, `tabs`, cookies & storage, `screenshot`, `pdf`,
`route`, `console`, `network`, `dialog-*`, `attach`, sessions, config — plus
things playwright-cli has no equivalent for: `preview` (one-shot page health),
`collect` (auto-listing extraction), `perf` (Web Vitals), `assert` (CI exit
codes), built-in stealth, and transparent daemon (no `open` needed).

Explicit non-goals: WebKit/Firefox, `run-code` (Playwright runtime),
`pause-at`/`resume`/`step-over` (Playwright debug protocol), Playwright Trace
Viewer-compatible `trace.zip`.

Full parity matrix: [`docs/playwright-cli-parity.md`](docs/playwright-cli-parity.md).

---

## Using it with LLM agents

Same engine, with one installed runtime surface at a time:

1. **MCP stdio server** (`ghostchrome-mcp`) — 16 tools, the drop-in replacement for `@playwright/mcp`.
2. **Regular CLI** — allowlist `ghostchrome` for shell-tool agents.
3. **Typed SDKs** (`sdk/python`, `sdk/typescript`) — available with CLI mode and drive the persistent JSONL `agent` loop from code.

### Claude Code (Anthropic)

```bash
./install.sh --mode mcp --clients claude,codex,grok
```

Setup registers the standalone `ghostchrome-mcp` in Claude Code, Codex, and Grok. Add `GHOSTCHROME_CONNECT=auto` to its MCP environment when attaching to an already-running Chrome is required.

### Codex (OpenAI)

```bash
codex mcp list
```

### MCP tool surface (v2.0)

Deliberately small — 16 tools, no fat. Each one is on the hot path of a browser-driving loop.

| Tool | Purpose |
|---|---|
| `snapshot` | Status + errors + network + DOM with refs — canonical first call |
| `navigate` | Go to URL without snapshot |
| `click` | Click `@ref` |
| `type` | Type into `@ref` (`submit:true` to press Enter after) |
| `select` | Pick option in `<select>` by `@ref` |
| `press` | Send key (Enter, Tab, Escape, ArrowDown, ...) |
| `hover` | Hover an element by `@ref` (reveal dropdowns, tooltips) |
| `drag` | Drag from one `@ref` to another |
| `fill_form` | Bulk-fill form fields from `{ref: value}` JSON |
| `upload` | Attach files to an `<input type=file>` by `@ref` |
| `tabs` | List / switch / open / close browser tabs |
| `wait_for` | Wait for selector / text / timeout |
| `eval` | Run JS — escape hatch for anything else |
| `screenshot` | WebP/JPEG/PNG of viewport, full page, or element |
| `back` / `forward` | Browser history |

Niche workflows (cookies, storage, viewport, network sniff/replay, tracing) live in the CLI only. Reach them via `eval` or shell out when needed.

### MCP incident traces

For a reproducible MCP session, set `GHOSTCHROME_MCP_TRACE` to a file path:

```bash
GHOSTCHROME_MCP_TRACE=/tmp/ghostchrome-mcp.jsonl ghostchrome mcp --stealth
ghostchrome trace-replay --file /tmp/ghostchrome-mcp.jsonl --format json
```

Ghostchrome appends one JSONL record per completed `tools/call`, including the
operation, duration and success/error outcome. Arguments are recorded with a
conservative privacy policy: typed text, form values, JavaScript expressions,
upload paths, credentials/session/authentication fields and sensitive URL query
parameters are redacted. Trace files are kept owner-readable only (`0600`),
including pre-existing files. Trace failures are reported on stderr and never
change the JSON-RPC response on stdout. The same file is truncated (never
rotated or archived) before the next append when its current window reaches 24
hours or that entry would take it beyond 1 MiB; the entry that triggers the
reset is retained.
If a previous process left a malformed, partial, or unterminated JSONL line,
the next append replaces that broken window with the triggering entry so the
file is parseable again. A single encoded entry that would exceed 1 MiB is reduced to its safe
metadata (`op`, timing and outcome; no arguments, summary or error text) before
being written, so the hard cap still holds.

### Typed SDKs — Python & TypeScript

In-repo at [`sdk/python/`](sdk/python) and [`sdk/typescript/`](sdk/typescript). Each is a thin, typed client that spawns a persistent `ghostchrome agent` subprocess and speaks its JSONL protocol over stdio, so refs (`@1`, `@2`) and session state persist across calls. Result types are matched to what the binary actually emits (re-measured with [`scripts/measure-agent-ops.sh`](scripts/measure-agent-ops.sh), never guessed).

> **Not published to any package registry yet.** The SDK source lives in this repo
> (and in the `v0.1.0` source tarball), but the packages are **not** on npm or PyPI —
> so `npm install @ghostchrome/sdk` / `pip install ghostchrome` do **not** work yet.

| Channel | Status | How to install |
|---|---|---|
| GitHub repo — `sdk/python`, `sdk/typescript` | ✅ available | clone, or `pip install "git+…#subdirectory=sdk/python"` (below) |
| npm — `@ghostchrome/sdk` | ❌ not published | — |
| PyPI — `ghostchrome` | ❌ not published | — |

Both SDKs require the `ghostchrome` binary on `PATH`.

```python
# pip install "git+https://github.com/dev-toolings/ghostchrome.git#subdirectory=sdk/python"
from ghostchrome import Ghostchrome

with Ghostchrome(extra_flags=["--connect=auto"]) as gc:
    nav, _ = gc.navigate("https://example.com")
    print(nav.status, nav.title)            # 200, "Example Domain"
    tree, _ = gc.extract(level="skeleton")
    print(tree.stats.interactive_count)     # @ref count
    gc.click("@1")
```

```ts
// build + local install: cd sdk/typescript && bun run build && bun add /path/to/sdk/typescript
import { createGhostchrome } from "@ghostchrome/sdk";

const gc = createGhostchrome({ flags: ["--connect=auto"] });
const { result } = await gc.navigate("https://example.com");
console.log(result.status, result.title);
const dom = await gc.extract({ level: "skeleton" });
await gc.close();
```

Runnable end-to-end examples (both languages) live in [`examples/`](examples).

### Custom loop — shell-out, zero SDK

```python
import subprocess, json
def snapshot(url):
    r = subprocess.run(
        ["ghostchrome", "preview", url, "--connect=auto", "--json"],
        capture_output=True, text=True, check=True,
    )
    return json.loads(r.stdout)
```

### Aider / Cursor / any agent with shell access

Use `ghostchrome` as a regular shell command. The daemon starts automatically — no `serve` step.

---

## Command reference

<details>
<summary>Click to expand the full command surface</summary>

```text
Page inspection
  preview <url>                 Page health: status, errors, network, DOM
  navigate <url>                Navigate; optionally extract
  extract  <url>                Compact accessibility tree with refs
  screenshot <url>              PNG of viewport, full page, or element
  eval "<expr>" <url>           Run JS, await async, return value
  errors <url>                  Console + Log + network 4xx/5xx
  perf <url>                    Lighthouse-lite timing summary

Interaction (refs from the last snapshot)
  click @N <url>
  dblclick @N <url>             Double-click an element
  type @N "text" [--submit]     Type; --submit presses Enter after
  fill-form <json>              Bulk fill {@ref: value}
  check @N / uncheck @N         Idempotent checkbox / radio toggle
  select @N "option" <url>
  hover @N <url>
  drag @from @to                Drag-and-drop between refs
  press <key> [--on @N] <url>
  upload @N <file...>           Attach files to a file input

Browser & session
  serve [--port N]              Long-lived Chrome; prints ws:// URL
  tabs                          List tabs
  tabs new [url]                Open + activate a new tab
  tabs switch <i> / close <i>   Switch / close a tab by index
  reload                        Refresh the current page
  back / forward
  waitfor "selector" <url>
  import-profile                Clone an existing Chrome profile (cookies)
  doctor                        Diagnose setup (Chrome, profiles, connectivity)

Scraping & bulk
  batch <jsonl>                 Run agent ops from a JSONL file
  fastfetch <url>               HTML-only fast path, no JS render
  collect <url>                 Observer stream (NDJSON of net+console+page events)

Agents
  agent                         Drive the browser from JSONL ops on stdin
  mcp                           Run as an MCP server (stdio, 16 tools)
```

Full details: [`docs/cli.md`](docs/cli.md).

</details>

---

## Playwright CLI parity

ghostchrome exposes Playwright CLI-compatible command names for the core
browser loop where the behavior maps cleanly to existing CDP/Rod primitives:
`open`, `snapshot`, `fill`, `resize`, `go-back`, `go-forward`, `state-save`,
`state-load`, `attach --cdp=<channel|url>`, `cookie-*`, `localstorage-*`,
`sessionstorage-*`, `dialog-*`, `tab-*`, session management aliases, and raw
mouse/key aliases. The current `@playwright/cli` 0.1.18 baseline resolves all
86 public command names. Recent compatibility work includes real `drop`,
snapshot `find`/`--boxes`, strict CSS/ref/locator targets, native-DPR
`screenshot --hires`, `open --mobile/--device`, persistent network/console
history, visual `highlight`, and `show --annotate` artifacts.
`video-start`/`video-stop` also record across separate CLI invocations through a
daemon-attached runtime; the honest artifact is a JPEG frame sequence plus a
manifest, not a WebM file.

Output can be shaped with `--json`/`--raw`, bounded with
`--output-max-size` (or `PLAYWRIGHT_MCP_OUTPUT_MAX_SIZE`), and redacted from a
dotenv secrets file before it reaches stdout or an overflow artifact. Structural
Playwright-runtime features such as Firefox/WebKit, `run-code`, debugger stepping,
and Trace Viewer-compatible archives remain explicit `unsupported` boundaries.

The tracked source-of-truth matrix is [`docs/playwright-cli-parity.md`](docs/playwright-cli-parity.md).
It separates compatible commands from partial matches and explicit gaps so the
project does not claim parity that is not implemented.

---

## Status & roadmap

**Stable** — preview, navigate, extract, click/type/select/hover/press, errors, screenshot, eval, serve, `--connect=auto`, MCP server (16 tools), JSONL `agent` loop, typed Python & TypeScript SDKs.

**Experimental** — stealth patches, AI extractors, opt-in content-boundary fencing. Tracked behind flags; APIs may change.

**Not in scope (yet)** — Firefox/WebKit support (would arrive via a `playwright-core` subprocess fallback, not native), GUI test runner, visual regression diff.

Versioning follows SemVer; see [`.claude/rules/versioning.md`](.claude/rules/versioning.md).

---

## Contributing

PRs welcome. The codebase is small and laid out in [`engine/`](engine/) (CDP logic) and [`cmd/`](cmd/) (one Cobra command per file). Run tests with `go test ./...`. Benchmark changes should include auditable raw samples from [`benchmark/cli-measure.mjs`](benchmark/cli-measure.mjs) and [`benchmark/mcp-measure.mjs`](benchmark/mcp-measure.mjs), with the browser and opponent package versions pinned.

When the agent surface changes, **re-measure the live binary** with [`scripts/measure-agent-ops.sh`](scripts/measure-agent-ops.sh) and update the in-repo SDKs at [`sdk/typescript/`](sdk/typescript) and [`sdk/python/`](sdk/python) so their result types match what the binary emits — never guess. See [`CLAUDE.md`](CLAUDE.md).

---

## License

[MIT](LICENSE) © 2026 MakFly.


### Optional HTTP TLS fallback

`fastfetch --fallback-tls <url>` tries ordinary HTTP first, then one in-process
Chrome 146 TLS/HTTP2 request if blocked or missing structured SSR data. Add
`--fallback-browser` to allow Chrome afterward. The default remains unchanged.
The JSON envelope reports `mode: "http-tls"` when that transport supplies the
response. Go recipes can set `engine.FastFetchOpts{FallbackTLS: true}`.

This fallback needs no Python/curl executable and imports no browser session.
Its default headers match Chrome 146/macOS. Both HTTP attempts share the timeout;
401, 429 and ordinary 404 responses are not retried by the TLS fallback.
Certificates remain verified, redirects stay on the same origin, and response
size limits remain enforced. No GPU, marketplace, or notification logic is part
of this transport. It is available through `fastfetch` and the Go engine, not a
new MCP or agent operation.
