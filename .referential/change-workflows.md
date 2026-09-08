# Change Workflows

## General Rule

Start from the narrowest source of truth:

1. User-facing CLI behavior: inspect `internal/surface/cli/<command>.go`,
   `internal/surface/cli/root.go`,
   `docs/cli.md`, and any engine helper it calls.
2. Browser behavior: inspect the relevant `internal/core/engine/` file and its tests.
3. JSONL/MCP/AI op exposure: `internal/ops/ops.go` declares it and generates
   the registrations; the handler bodies live in `internal/runtime/ops.go` and
   `internal/surface/mcp/tools.go`.
4. SDK behavior: inspect `contracts/commands.json` plus SDK tests and client
   wrappers.

Keep `internal/surface/cli/` as thin glue. Shared behavior belongs in
`internal/core/engine/`.

## Changing A CLI Command

Checklist:

- Update the command implementation in `internal/surface/cli/`.
- If it changes shared browser behavior, move or update logic in `internal/core/engine/`.
- Update `docs/cli.md` and `README.md` when user-facing flags, examples, or
  output change.
- If the change affects agent ops or JSON output shapes, follow the agent
  surface workflow below.
- Run targeted Go tests first, then broader validation as needed.

## Changing Agent Ops, MCP Tools, Or SDK Contracts

This is the high-risk path because several surfaces must stay aligned.

1. Edit `internal/ops/ops.go`, including the MCP/AI surface specs if the op is
   exposed there.
2. Run `go generate ./internal/ops/...`. It rewrites `contracts/commands.json`
   and the three registration tables.
3. Write the handler body each generated binding names; until then the build
   fails, which is the intended signal.
4. Measure the live binary result shapes with `scripts/measure-agent-ops.sh`
   before editing SDK result types.
5. Update `sdk/typescript/src/` and `sdk/python/ghostchrome/` wrappers and
   types as needed.
6. Update SDK tests, especially contract coverage tests.
7. Update examples if method signatures or expected shapes changed.

Important: do not guess SDK result shapes from `contracts/commands.json`.
Measure them from the binary.

## Changing Engine Behavior

Typical target files:

- Navigation or load strategy: `internal/core/engine/navigator.go`, `internal/core/engine/wait.go`.
- Ref extraction: `internal/core/engine/extractor.go` and fallback/extractor tests.
- Click/type/select/press: `internal/core/engine/interactor.go`, locator and wait helpers.
- Errors/network reporting: `internal/core/engine/errors.go`, observer/network tracker files.
- Stealth and anti-bot behavior: `internal/core/engine/stealth.go`,
  `internal/core/engine/antibot_blocker.go`, `internal/core/engine/human.go`.
- Session/profile handling: `internal/core/engine/browser.go`, session registry/profile
  files.

Add or adjust focused engine tests when behavior is deterministic. For browser
or site-dependent behavior, prefer a targeted smoke run rather than broad
claims.

## Changing SDKs

TypeScript:

- Package manager: `bun`.
- Primary paths: `sdk/typescript/src/`, `sdk/typescript/tests/`.
- Validation: `cd sdk/typescript && bun install && bunx tsc --noEmit && bun test`.

Python:

- Runtime dependency policy: stdlib only.
- Primary paths: `sdk/python/ghostchrome/`, `sdk/python/tests/`.
- Validation: `cd sdk/python && python3 -m unittest discover -s tests -q`.

SDKs communicate with `ghostchrome agent` over JSONL stdio. They should remain
thin typed clients, not alternate implementations of browser behavior.

## Documentation Drift Notes

The current local files supersede older references. In this checkout:

- The SDKs are in-repo, not in a sibling `../ghostchrome-sdk` directory.
- The current contract path is `contracts/commands.json`.
- `CLAUDE.md` is more specific than the older generic AGENTS note for SDK
  synchronization.

