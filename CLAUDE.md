# CLAUDE.md — ghostchrome

## Project overview

ghostchrome is an ultra-light CLI browser automation tool written in Go, designed for LLM agents. It uses Chrome DevTools Protocol (CDP) via Rod to control Chrome headless and returns compact output optimized for minimal token usage.

## Local referential

Agent-facing project context lives in `.referential/`. Read `.referential/README.md`
first for the current source-of-truth map, then use `.referential/project-map.md`,
`.referential/change-workflows.md`, and `.referential/validation.md` as the local
working reference for architecture, change workflows, and validation scope.

## Architecture

```
cmd/ghostchrome/ -> internal/surface/cli/ -> internal/core/engine/ -> Chrome
```

- `cmd/ghostchrome/`, `cmd/ghostchrome-mcp/` — thin mains, nothing else lives here
- `skillbundle.go` — the repo-root package that owns the `//go:embed` of
  `.claude/skills/ghostchrome`; embed patterns cannot escape their own directory
- `internal/surface/cli/*.go` — One file per cobra command
- `internal/surface/mcp/`, `internal/surface/ai/` — the other wire surfaces
- `internal/runtime/` — one op implementation, shared by every surface
- `internal/setup/` — installation, transports, skills, diagnostics
- `internal/compat/playwright/` — the Playwright CLI config contract
- `internal/core/engine/browser.go` — Browser lifecycle (connect/launch/close)
- `internal/core/engine/navigator.go` — Page navigation with wait strategies
- `internal/core/engine/extractor.go` — CDP Accessibility tree → compact DOM with refs (@1, @2)
- `internal/core/engine/interactor.go` — Click, type, hover, select, press, viewport, tabs, dialog
- `internal/core/engine/errors.go` — Console + network error collection
- `internal/core/engine/preview.go` — All-in-one page health report
- `internal/core/engine/stealth.go` — Anti-detection patches
- `internal/core/engine/cookies.go` — Cookie banner auto-dismiss
- `internal/ops/` — Canonical op catalog (single source of truth); `go generate` emits `contracts/commands.json` **and** the JSONL/MCP/AI registrations, so a surface cannot expose an op the catalog does not declare
- `contracts/commands.json` — Generated op contract the SDKs are typed against
- `sdk/typescript/`, `sdk/python/` — In-repo typed SDKs; thin clients that spawn a persistent `ghostchrome agent` subprocess and speak the JSONL protocol over stdio
- `examples/` — Runnable end-to-end examples (TS + Python) attaching via `--connect=auto`
- `packages/<site>/` + `internal/surface/cli/<site>.go` — Site scrapers, **gitignored** (kept on disk, never committed), compiled in only via `go build -tags recipes ./cmd/ghostchrome`

## Build & test

```bash
go build -o ghostchrome ./cmd/ghostchrome
go test ./internal/core/engine/...
./ghostchrome preview https://example.com
```

## Key design decisions

- **CLI over MCP**: CLI is the 2026 trend for browser-LLM integration. Simpler, no JSON-RPC overhead.
- **Rod over chromedp**: Decode-on-demand, no zombie processes, native iframe support.
- **Filtered accessibility tree**: Only interactive elements get refs. 7-25x fewer tokens than full a11y tree.
- **Three extraction levels**: skeleton (minimal) / content (text) / full (everything named).
- **Transparent daemon**: Every command auto-spawns a persistent background Chrome
  on first use (session "default"), matching Playwright CLI behavior. No `serve`,
  no `--connect`, zero config. Opt-out with `GHOSTCHROME_NO_DAEMON=1`.

## Runtime policy (preferred mode)

**Transparent daemon by default.** Every command auto-spawns (or reuses) a persistent
background Chrome via the implicit "default" session. No manual `serve` or `--connect`
needed. The daemon Chrome lives under `~/.ghostchrome/profiles/default` and persists
across CLI invocations until explicitly stopped (`sessions stop default`) or the
machine reboots.

Rationale:
- avoids Chrome startup cost per command (~hundreds of ms)
- preserves session state (cookies, storage, open tabs) across ops
- reduces fingerprint variance vs. fresh-profile spawns
- matches Playwright CLI behavior (zero-config daemon)

When designing new commands, flags, or SDK call paths, assume the implicit daemon is
the default execution context. `--connect=auto`, explicit `--connect ws://...`, and
`-s <name>` are overrides for advanced use cases. Cold spawn is a documented escape
hatch only (via `GHOSTCHROME_NO_DAEMON=1`).

## Installation modes and agent skill

`ghostchrome setup --mode cli|mcp` (internal/setup) installs exactly one local transport. CLI mode
installs `ghostchrome`; MCP mode installs the standalone `ghostchrome-mcp` and
registers it for the selected global clients. `setup switch --to ... --yes` is the
only supported mode transition. The canonical English skill lives under
`.claude/skills/ghostchrome/` and is copied unchanged to Claude, Codex, and Grok;
it reads `~/.ghostchrome/install.json` and must use exactly one transport per flow.
Use `ghostchrome setup doctor --strict` for installation/CDP diagnostics and keep
global AGENTS/CLAUDE instruction edits explicit with `setup instructions --write`.

## Conventions

- Language: English for code, comments, and commits
- Commits: Conventional Commits (`feat:`, `fix:`, `chore:`, etc.)
- Package manager: Go modules only
- No external runtime dependencies (single static binary)

## Versioning

Follow SemVer (vMAJOR.MINOR.PATCH):
- MAJOR: Breaking CLI interface changes (renamed commands, changed output format)
- MINOR: New commands or flags (backward compatible)
- PATCH: Bug fixes, performance improvements

## SDK synchronization

The SDKs live **in this repo** under `sdk/typescript/` and `sdk/python/` (TypeScript
and Python only — no external `../ghostchrome-sdk` repo, no PHP). They are typed
against the generated contract and driven by the JSONL `agent` loop.

Status: both SDKs are at **v0.5.0**, cover **100% of the JSONL-surface ops**, are
publish-ready (TS `bun run build` → `dist/`; Python ships `py.typed`), and their
result types are matched to what the binary actually emits.

### Stay in the truth — ALWAYS re-measure, never guess

The op *names / args / surfaces* have a single source of truth: `internal/ops/`
(the canonical catalog, which generates `contracts/commands.json`). But the op
**result shapes** are owned by the running binary, not by any hand-written type.
Past SDK drift (e.g. `extract.stats` had `{total,interactive}` while the binary
emits `{total_nodes,filtered_nodes,interactive_count}`; `errors` returns a top-level
array, not `{errors:[]}`; empty ops OMIT `result`) all came from *guessing*.

Rule: before changing the SDK or the contract, **measure the live binary**:

```bash
# 1. start a Chrome the agent can attach to
google-chrome --headless=new --remote-debugging-port=9222 --user-data-dir=/tmp/gc-measure about:blank &
# 2. print the REAL result shape of every JSONL op
scripts/measure-agent-ops.sh
```

The shapes it prints are ground truth (read from the binary, cannot drift). Make
the SDK types match that output, not your assumptions.

### Change workflow

1. Edit `internal/ops/ops.go` (the catalog), then `go generate ./internal/ops/...`
   to regenerate `contracts/commands.json`, `internal/runtime/handlers_gen.go`,
   `internal/surface/mcp/tools_gen.go` and `internal/surface/ai/tools_gen.go`.
   Then write the handler body the generated binding points at.
2. **Re-measure** with `scripts/measure-agent-ops.sh` and reconcile result shapes.
3. Update the typed wrappers in `sdk/typescript/src/` and `sdk/python/ghostchrome/`
   plus their hermetic tests (`bun test`, `python -m unittest discover -s tests`).
   Each SDK has a contract-coverage test asserting every JSONL op has a method.
4. Update or add an `examples/` script if usage changed; re-run the e2e
   (`just e2e` against a live Chrome) so real shapes flow through both SDKs.
5. The three surfaces are generated, so they cannot drift from the catalog.
   `internal/ops/gen` has the one test that still matters: it fails if the
   committed generated files are not what the current catalog produces.

Build/test everything via the root `justfile` (`build`, `test`, `test-all`,
`contract`, `e2e`) or directly: `go test ./...`, then the two SDK suites.
