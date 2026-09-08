# CLAUDE.md: ghostchrome

## Project overview

ghostchrome is an ultra-light CLI browser automation tool written in Go, designed for LLM agents. It uses Chrome DevTools Protocol (CDP) via Rod to control Chrome headless and returns compact output optimized for minimal token usage.

## Local referential

Agent-facing project context lives in `.referential/`. Read `.referential/README.md`
first for the current source-of-truth map, then use `.referential/project-map.md`,
`.referential/change-workflows.md`, and `.referential/validation.md` as the local
working reference for architecture, change workflows, and validation scope.

## Keep this document current

This file is a maintained project reference. Update it as part of the same change
whenever you add, remove, rename, or move a directory, change module ownership,
alter a runtime or installation flow, change a development command, or change
the SDK, contract, generation, or release workflow. Do not leave this work for a
later cleanup or wait for the user to request a documentation update.

Before completing a task:

1. Compare the final implementation and repository layout with this document.
2. Correct affected paths, responsibilities, commands, and policies here.
3. Update the relevant pages in `.referential/` and `docs/`, and the public
   `README.md` when the change affects their content.
4. Check that documented paths exist and commands match the current scripts.
   Report what you actually validated; do not claim unrun checks passed.

Describe the current implementation separately from proposed work. Verify facts
against source files and executable behavior rather than copying historical
reports. Read versions from package manifests and coverage from tests instead
of keeping an unverified status snapshot here.

`AGENTS.md` is a symlink to `CLAUDE.md`. Preserve that link so both agent entry
points use the same instructions. Keep this entire document in English.

## Repository architecture

```text
ghostchrome/
+-- cmd/
|   +-- ghostchrome/          CLI entry point
|   +-- ghostchrome-mcp/      Standalone MCP entry point
+-- internal/
|   +-- surface/
|   |   +-- cli/             Cobra commands and JSONL agent loop
|   |   +-- mcp/             MCP protocol adapter
|   |   +-- ai/              LLM tool adapter
|   +-- runtime/             Shared operation dispatch and handlers
|   +-- core/                Browser engine and supporting packages
|   +-- ops/                 Operation catalog and generators
|   +-- setup/               Installation, skills, and diagnostics
|   +-- compat/playwright/   Playwright CLI configuration contract
+-- contracts/               Generated operation contract
+-- sdk/
|   +-- typescript/          Typed TypeScript JSONL client
|   +-- python/              Typed Python JSONL client
|   +-- examples/            SDK and integration examples
|   +-- npm/                 CLI distribution package manifests and launcher
+-- tools/
|   +-- benchmark/           Fixtures, runners, reports, and comparison tools
|   +-- deploy/              Deployment helpers, including SearXNG
+-- scripts/                 Installers and validation scripts
+-- docs/                    Documentation, architecture audit, and plans
+-- recipes/                 Private local scraping implementations
+-- .claude/skills/ghostchrome/  Canonical embedded agent skill
+-- .github/workflows/       CI and release automation
+-- .referential/            Agent reference and validation guides
+-- .local/                  Ignored binaries, captures, and scratch notes
```

### Ownership and execution

- `cmd/` contains thin executable entry points. Application logic belongs in
  `internal/`.
- `internal/surface/cli/` owns Cobra commands and the JSONL agent loop.
  `internal/surface/mcp/` and `internal/surface/ai/` adapt their respective
  protocols. Shared catalog operations use `internal/runtime/`; CLI commands
  may also call core packages directly.
- `internal/runtime/` owns the shared operation handlers and dispatch table.
  SDKs spawn a persistent `ghostchrome agent` subprocess and exchange JSONL
  messages over standard input and output.
- `internal/core/engine/` owns browser lifecycle, navigation, extraction,
  interaction, and observation through Rod and CDP. Key files include
  `browser.go`, `navigator.go`, `extractor.go`, `interactor.go`,
  `errors.go`, `preview.go`, and `stealth.go`.
- Supporting core packages are `antibot`, `artifact`, `coretest`,
  `dashboard`, `feedback`, `inspect`, `interact`, `media`, `overlay`,
  `pagesetup`, `policy`, `provider`, `proxy`, `sites`, `storage`,
  and `vault`. Keep features with their owning package.
- `internal/ops/ops.go` is the canonical operation catalog. Its generators
  emit `contracts/commands.json`, `internal/runtime/handlers_gen.go`,
  `internal/surface/mcp/tools_gen.go`, and
  `internal/surface/ai/tools_gen.go`. Change the catalog and generator
  inputs, then regenerate; do not hand-edit generated files.
- `internal/setup/` owns installation modes, transports, skills, and
  diagnostics. The root `skillbundle.go` embeds
  `.claude/skills/ghostchrome/` because Go embed patterns cannot escape
  their declaring package directory.
- Private scrapers live under `recipes/<site>/`, with command adapters under
  `internal/surface/cli/`. Keep private recipe files gitignored and compile
  their adapters only with `go build -tags recipes ./cmd/ghostchrome`.

### Keep the root small

Keep root files only when they are required by tooling or provide a public entry
point: Go module files, `skillbundle.go`, Bun workspace files, `justfile`,
`install.sh`, repository configuration, `README.md`, `CHANGELOG.md`,
`LICENSE`, and agent instructions.

Place new documentation and reports in `docs/`, development and deployment
tools in `tools/`, SDK examples in `sdk/examples/`, and npm distribution
sources in `sdk/npm/`. Place local binaries in `.local/bin/` and local
captures or scratch notes in `.local/`. Do not commit private artifacts or
recreate the former root-level benchmark, deploy, or examples directories.

Bun manages the root `node_modules/` directory. Release CI writes generated
artifacts to the ignored root `dist/` directory; npm package sources stay in
`sdk/npm/`.

## Build and validation

Run from the repository root unless a command explicitly changes directories:

```bash
# Compile all packages without emitting a root binary.
go build ./...

# Build a local executable.
go build -o .local/bin/ghostchrome ./cmd/ghostchrome

# Run the hermetic Go suite.
go test -short ./...

# Check private recipe compilation when changing its paths or adapters.
go build -tags recipes ./...

# Run the SDK suites.
(cd sdk/typescript && bun install && bunx tsc --noEmit && bun test)
(cd sdk/python && python3 -m unittest discover -s tests -q)
```

The root `justfile` provides `build`, `test`, `test-all`, `contract`,
`install`, and `e2e` shortcuts. If `just` is unavailable, run the equivalent
commands directly. Use targeted checks for narrow changes. Browser integration
checks require Chrome; `just e2e` requires an attachable Chrome on port 9222.
Documentation-only changes require path and diff checks, not a full test run.

### Continuous integration

`.github/workflows/ci.yml` runs on branch pushes, version tags, pull requests
to main, and manual dispatch. It checks Linux/macOS builds with race detection,
lint, the 10,000-operation browser loop, Linux integration tests, macOS and
Windows smoke tests, and both SDKs.

`.github/workflows/security-audit.yml` installs the frozen Bun workspace with
scripts disabled and runs `bun audit --audit-level=high`, plus the JavaScript
configuration scan. It runs daily, manually, and for relevant manifest,
lockfile, workflow, or configuration changes. Installation and audit errors
must fail the job; do not mask them or substitute another package manager.

Use Go from `go.mod` and the Bun and golangci-lint versions pinned in the
workflows when reproducing CI. Verify the branch CI and security runs before
pushing a release tag, then follow the tag's CI, security, and release runs.

## Key design decisions

- **CLI-first interface**: Shell integration is the default; MCP is an alternative transport.
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
- Dependencies: Go modules for Go; Bun for the JavaScript workspace. Do not use npm or npx.
- The Go executable is a single static binary; browser operations require Chrome.
- Use English for documentation and project instructions as well.
- Do not use em dashes or en dashes as sentence asides. Rewrite with commas,
  parentheses, a colon, or separate sentences.

## Versioning

Follow SemVer (vMAJOR.MINOR.PATCH):
- MAJOR: Breaking CLI interface changes (renamed commands, changed output format)
- MINOR: New commands or flags (backward compatible)
- PATCH: Bug fixes, performance improvements

Release versions come from Git tags and are embedded with `-X main.version`.
When preparing a release, update `CHANGELOG.md`, the TypeScript SDK manifest,
all `sdk/npm/*/package.json` versions and internal optional dependencies, and
both the Python `pyproject.toml` version and `ghostchrome/__init__.py` version.
Run `bun install` and verify workspace versions in `bun.lock` match the
manifests; version-only changes can retain old workspace metadata. Build and validate before
pushing the release tag; the tag triggers binary, npm, and PyPI release jobs.

## SDK synchronization

The SDKs live **in this repo** under `sdk/typescript/` and `sdk/python/` (TypeScript
and Python only: no external `../ghostchrome-sdk` repo, no PHP). They are typed
against the generated contract and driven by the JSONL `agent` loop.

### Stay in the truth: ALWAYS re-measure, never guess

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
# 2. build the binary being measured and inspect the script's sampled ops
go build -o .local/bin/ghostchrome ./cmd/ghostchrome
GHOSTCHROME_BIN="$PWD/.local/bin/ghostchrome" scripts/measure-agent-ops.sh
```

The measured shapes are evidence for that binary and those sampled operations.
Measure any changed operation missing from the script separately. Make SDK types
match the observed output, not assumptions.

### Change workflow

1. Edit `internal/ops/ops.go` (the catalog), then `go generate ./internal/ops/...`
   to regenerate `contracts/commands.json`, `internal/runtime/handlers_gen.go`,
   `internal/surface/mcp/tools_gen.go` and `internal/surface/ai/tools_gen.go`.
   Then write the handler body the generated binding points at.
2. **Re-measure** with `scripts/measure-agent-ops.sh` and reconcile result shapes.
3. Update the typed wrappers in `sdk/typescript/src/` and `sdk/python/ghostchrome/`
   plus their hermetic tests (`bun test`, `python -m unittest discover -s tests`).
   Each SDK has a contract-coverage test asserting every JSONL op has a method.
4. Update or add an `sdk/examples/` script if usage changed; re-run the e2e
   (`just e2e` against a live Chrome) so real shapes flow through both SDKs.
5. Run `go test ./internal/ops/...` to verify catalog consistency and generated
   file freshness, alongside the relevant runtime and SDK tests.

Build/test everything via the root `justfile` (`build`, `test`, `test-all`,
`contract`, `e2e`) or directly: `go test ./...`, then the two SDK suites.
