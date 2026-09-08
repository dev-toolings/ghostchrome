# Changelog

All notable changes to ghostchrome are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/); the project follows SemVer.

## [Unreleased]

## [0.7.1] - 2026-09-08

### Fixed
- Audit the Bun workspace from its frozen lockfile instead of attempting npm
  and Yarn installs of platform-specific distribution packages.
- Run the complete CI suite on development branches and release tags, including
  browser integration and TypeScript/Python SDK validation.
- Use launcher-owned temporary Chrome profiles in runtime regression tests so
  shutdown finishes before profile cleanup.
- Document the intentional trusted-operator URL boundary at the two FastFetch
  request sites flagged by the SSRF linter.


## [0.7.0] - 2026-09-08

### Changed
- Group benchmarks and deployment helpers under `tools/`, examples and npm
  distribution sources under `sdk/`, and architecture reports under `docs/`.
- Build local executables under `.local/bin/` and keep captures and scratch
  notes in the ignored `.local/` directory.
- Document module ownership and require agents to keep project references current.

### Added
- **MCP `emulate` tool** : device emulation is now reachable from the MCP
  surface, not only the CLI. It takes a preset (`iphone-14-pro-max`, `pixel-7`,
  `ipad`, ...) or explicit `width`/`height`/`device_scale_factor`/`mobile`/
  `touch`/`user_agent`/`color_scheme`, and `reset: true` returns to a plain
  desktop tab. Until now an MCP agent had no way to change the viewport, so a
  mobile shell gated on a width breakpoint or `pointer: coarse` could not be
  tested at all: the page was always 1920x1080 with a fine pointer. The server
  holds the profile and replays it whenever it binds a new page (tab switch,
  popup, crash relaunch), because CDP emulation overrides die with the target.
- **MCP `swipe` tool** : a real single-finger touch gesture
  (`touchstart` → N × `touchmove` → `touchend`) between two viewport
  coordinates, via `Input.dispatchTouchEvent`. `drag` synthesizes mouse events,
  which a touch-only handler in a mobile drawer, carousel, or pull-to-refresh
  never receives. Touch emulation is enabled on demand so a swipe is never
  silently discarded.
- **`engine.SwipeTouch` / `TapTouch` / `EnsureTouchEmulation` /
  `ApplyEmulationProfile`** : the touch and emulation primitives behind the two
  tools. `ApplyEmulationProfile` differs from the additive
  `ApplyEmulationState`: it also turns touch emulation *off* when the profile
  asks for it, so going back to a desktop profile really restores
  `pointer: fine`.

### Fixed
- Enforce navigation policy before opening a new tab and reject init-script
  removal paths outside the script directory. Installed scripts now run before
  document scripts and remain registered across navigations.
- Mask configured secrets in JSONL payloads and saved snapshot artifacts.
- Merge independent session-state updates under a file lock and serialize MCP
  emulation updates to preserve concurrent changes.
- Preserve pre-init dialog policies and retained console/network errors. Reject
  malformed JSONL arguments and report post-action snapshot failures as
  non-retryable errors instead of claiming that the page is unchanged.
- Refresh command snapshots at the requested extraction level, including DOM
  changes at the same URL. Always dispose the TypeScript transport after close.
- **`--stealth` no longer hides an emulated touchscreen** : the stealth patch
  pinned `navigator.maxTouchPoints` to 0 on every new document, overwriting the
  value `Emulation.setTouchEmulationEnabled` installs. An emulated iPhone
  therefore claimed `pointer: coarse` while reporting zero touch points, and a
  mobile web app feature-detecting on `maxTouchPoints` kept its desktop shell.
  The pin is gone: a headless desktop Chrome already reports 0 on its own, and
  the contradiction was a stronger bot signal than the real number.

## [0.5.0] : 2026-08-12

### Fixed
- **Viewport and emulation no longer reset between commands** : CDP emulation
  overrides live in the DevTools session, not in the page, so Chrome dropped
  them as soon as a CLI process exited. Against the persistent daemon that made
  `ghostchrome viewport 390 844` a no-op for every following command: the page
  snapped back to the daemon window size (1280x800) and a "mobile" screenshot
  was really a desktop one. Managed sessions (the implicit daemon and
  `-s <name>`) now persist the requested profile (viewport, DPR, mobile, touch,
  UA, color-scheme, timezone) in their session state and replay it on attach.
  A Chrome you attached to yourself with `--connect` / `--connect=auto` is left
  untouched, as the runtime policy requires.
- **Non-touch device presets** : `emulate --device desktop` (and every other
  preset with `touch: false`) failed with `Touch points must be between 1 and
  16`: `maxTouchPoints` was sent while disabling touch emulation, which Chrome
  rejects. The field is now only sent when enabling.
- **Stale `SingletonLock` no longer bricks a session** : a Chrome that was
  killed rather than closed (`sessions stop`, crash, reboot) left its lock in
  the profile, and every later spawn aborted with "Failed to create
  SingletonLock: File exists". Session spawn now removes the lock when its
  owning PID is gone; a live owner, or a lock written by another host, is never
  touched.

### Added
- **`emulate --reset`** : drops every emulation override (viewport, touch, UA,
  color-scheme, timezone) on the page and clears the session's persisted
  profile, back to a plain un-emulated tab.

## [0.4.0] : 2026-07-20

### Added
- **Opt-in daemon idle shutdown** : set `GHOSTCHROME_IDLE_TIMEOUT` (a Go
  duration like `30m`, or bare seconds) and a `serve` daemon exits after that
  long with no browser activity (tracked via the CDP target set). Off by
  default, so the persistent-daemon runtime policy is unchanged; bounds the
  disk/RAM growth of a forgotten daemon.
- **`profiles gc`** : reclaim stale Chrome profiles. Dry-run by default; only
  targets profiles that are not the `default` daemon profile, not backing a
  live session, and idle past `--older-than` (default 168h). Delete with
  `--yes`. Login profiles still in rotation are preserved by the idle gate.

### Changed
- **Static binaries** : release and local (`just install`) builds now set
  `CGO_ENABLED=0`, producing a truly statically linked binary (no libc/ld
  dependency), matching the "single static Go binary" promise. Local builds
  also stamp the real version via `-X main.version` instead of `dev`.
- **Version coherence** : all in-repo package manifests (npm CLI + platform
  packages, TypeScript SDK, Python SDK) and the docs now report `0.3.0`,
  removing the prior `0.1.0`/`0.2.0`/`0.3.0` drift. Release CI now also builds
  and publishes the typed SDKs (`@ghostchrome/sdk`, PyPI `ghostchrome`) on tag,
  gated on `NPM_TOKEN`/`PYPI_TOKEN`.

### Fixed
- **JSONL/SDK extract payload no longer duplicates subtrees** : the `agent`
  JSONL protocol (and `extract --json` / MCP) serialized every interactive
  node's full `children` subtree twice: once in `nodes` and again inside each
  `refs` entry. `ExtractionResult` now strips `children` from `refs` on the
  wire (the in-memory struct is untouched, so `ExtractionForRef` still works),
  turning `refs` into a flat index. Measured ~15% smaller extract JSON
  (~8k fewer tokens) on a rich page; the SDK's `RefEntry` type already assumed
  this shape.
- **MCP tool-count comment** : the package doc claimed "11 essential tools"
  while 16 are registered; corrected to match reality.
- **TypeScript SDK repository URL** pointed at a non-existent
  `ghostchrome/ghostchrome`; fixed to `dev-toolings/ghostchrome`.
- **MCP survives Chrome death** : the MCP server held its browser/page
  singleton forever without re-validating it: when Chrome crashed, every
  tool call failed with `context deadline exceeded` until the server was
  restarted by hand. `ensurePageLocked` now pings the browser (cheap
  `Browser.getVersion`, 2s bound) before reuse; on a dead Chrome it tears
  down all per-browser state (snapshot/refs included) and relaunches a
  fresh browser transparently. If the relaunch itself fails, the tool
  returns an explicit `chrome process died and could not be relaunched`
  error instead of the opaque timeout.

## [0.3.0] : 2026-06-11

### Added
- **Bundled agent skill** : the Claude Code skill is embedded in the binary and
  installed globally to `~/.claude/skills/ghostchrome/` by the installer
  (`ghostchrome skills install/remove/status`); `uninstall` removes it.
- **Profile/disk management** : `profiles list` (sorted by size) and
  `profiles rm <name...>` to reclaim disk; `--purge` on `sessions stop`/
  `kill-all` deletes the profile with the session.
- **`ghostchrome uninstall [--purge] [--yes]`** : stops sessions, removes the
  binary + bundled skill, and with `--purge` the data dirs.

### Fixed
- **Robust session/process lifecycle** : stealth is best-effort with its own
  bounded context (no more `stealth: context canceled` abort on heavy sites);
  `serve` self-exits when its Chrome dies; respawn/prune/stop kill only a PID
  verified (by exact `serve --port <p> --user-profile <name>` tokens, per
  platform) to be this session : no orphan browsers, no subprocess pile-up.

## [0.1.0] : 2026-06-11

First release of the project. Single static Go binary that drives Chrome over
CDP for LLM agents.

### Added
- **Managed sessions (`-s` / `--session`)** : playwright-cli `-s` parity. A named
  session auto-spawns a persistent Chrome on first use (bound to a disk profile of
  the same name, cookies persist), reuses it : including the active tab : across
  calls, and needs no `ws://` URL. `$GHOSTCHROME_SESSION` sets the default; manage
  with `ghostchrome sessions list | stop <name> | kill-all`. `goto` added as an
  alias of `navigate`.
- **bun/npm install** : `bun install -g @ghostchrome/cli` (or npm). A meta package
  resolves the prebuilt Go binary for the host platform via os/cpu-gated
  optionalDependencies (no postinstall, bun-compatible). Published by CI on tag.
- **playwright-cli parity verbs**: `reload`, `dblclick @ref`, `check`/`uncheck @ref`
  (idempotent checkbox/radio), `tabs new [url]`, and `type --submit` (press Enter
  after typing). Wired across the CLI, the JSONL `agent` op surface, and both SDKs.
- Install docs aligned with `@playwright/cli`: one-line install + one-line
  `claude mcp add` agent wiring.

### Fixed
- engine: release `ProviderFunc` cleanup in `Browser.Close` (and on connect
  failure) : provider-provisioned Chrome was leaked.
- engine: write session state atomically (temp file + rename) to survive a
  crash mid-write.
- engine: strip credentials from the Chrome `--proxy-server` flag (auth via CDP).
- engine/mcp: `wait_for` is now cancellable via context instead of blocking
  sleeps that ignored client disconnects.
- cmd/agent: surface `extract` arg-parse errors; log stdout write failures;
  fill form fields in deterministic `@ref` order (was Go-map-random).
- ops: catalog the 5 MCP tools (`drag`, `fill_form`, `hover`, `tabs`, `upload`)
  that had drifted out of the parity guard, so `contracts/commands.json` matches
  the 16 tools the MCP server actually registers.
- sdk(ts): escalate `SIGTERM` → `SIGKILL` on dispose; default per-op timeout 30s.
- sdk(py): read agent stdout line-buffered instead of byte-by-byte.

### Core
- Single static Go binary that drives Chrome over CDP (via Rod) and returns
  compact, token-efficient output for LLM agents.
- Filtered accessibility extraction with stable `@ref` ids; three levels
  (`skeleton`, `content`, `full`).
- `preview` page-health report, `extract`, `errors`, `eval`, `screenshot`,
  interaction ops (`click`/`type`/`select`/`hover`/`press`), `serve`, and
  `--connect=auto` zero-spawn attach.
- Stealth patches, cookie-banner auto-dismiss, persistent profiles, encrypted
  state vault, domain policy, and a live monitoring dashboard.

### Agent surfaces
- **MCP server** (`ghostchrome mcp`) : 16 tools, a drop-in replacement for
  `@playwright/mcp`; standalone `ghostchrome-mcp` binary also shipped.
- **JSONL `agent` loop** : persistent stdin/stdout op protocol.
- **Canonical ops catalog** (`internal/ops`) as the single source of truth,
  generating `contracts/commands.json`, guarded by a parity test.

### SDKs (in-repo)
- **Python** (`sdk/python`) and **TypeScript** (`sdk/typescript`) typed clients
  that drive the persistent JSONL agent loop. Result types are measured against
  the live binary (`scripts/measure-agent-ops.sh`), not guessed.

### Other
- Opt-in content-boundary fencing for untrusted page text (prompt-injection
  defense) on the `extract` CLI.
- Site scrapers live out of the committed tree and compile in only via
  `go build -tags recipes .`.
- Runnable end-to-end examples under `examples/`.

[0.1.0]: https://github.com/dev-toolings/ghostchrome/releases/tag/v0.1.0
