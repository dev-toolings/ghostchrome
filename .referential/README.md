# ghostchrome Referential

This directory is a local agent referential for the current checkout of
`ghostchrome`. It summarizes the project shape, source-of-truth files, change
rules, and validation commands so future work can start from repo facts instead
of rediscovery.

Generated from local source inspection on 2026-06-13. Treat it as a working
map, not as a replacement for the canonical files listed below.

## Canonical Sources

| Concern | Source |
|---|---|
| Project overview and user-facing behavior | `README.md` |
| Architecture and module map | `docs/architecture.md` |
| CLI reference | `docs/cli.md` |
| MCP reference | `docs/mcp.md` |
| Agent/SDK development workflow | `CLAUDE.md` |
| Command/op catalog | `internal/ops/ops.go` |
| Generated command contract | `contracts/commands.json` |
| CLI command registration and global flags | `internal/surface/cli/root.go` |
| Go validation shortcuts | `justfile` |
| TypeScript SDK | `sdk/typescript/` |
| Python SDK | `sdk/python/` |

## Core Model

`ghostchrome` is a Go CLI browser automation tool for LLM agents. It controls
Chrome through CDP via Rod and optimizes output for compact agent loops.

The main execution chain is:

```text
cmd/ghostchrome -> internal/surface/cli (Cobra glue)
              -> internal/core/engine (browser/CDP logic) -> Chrome
```

The repo exposes three main automation surfaces:

1. CLI commands, for shell-driven browser automation.
2. MCP server, via `ghostchrome mcp` or `ghostchrome-mcp`, with a deliberately
   small tool surface.
3. JSONL `agent` loop, used by the Python and TypeScript SDKs.

## Current Repo Truths

- The SDKs currently live in this repository under `sdk/typescript/` and
  `sdk/python/`.
- The generated command contract is `contracts/commands.json`.
- `internal/ops/ops.go` is the canonical catalog for op names, args, summaries,
  and JSONL/MCP/AI surface exposure.
- Result shapes are not inferred from the catalog. They must be measured from
  the running binary with `scripts/measure-agent-ops.sh` when the agent surface
  or SDK types change.
- Preferred CLI runtime mode is the implicit persistent daemon. Explicit
  `--connect=auto` attaches to an existing Chrome; the JSONL agent embeds Chrome
  unless a named session or connection is requested.
- Use `bun` for JS/TS package commands. Do not use `npm` or `npx`.
- Use `rg` (ripgrep) or the built-in search tools for code search.

## Files In This Referential

- `project-map.md`: architecture, directories, surfaces, source ownership.
- `change-workflows.md`: how to change commands, engine behavior, SDKs, docs,
  and validation.
- `validation.md`: command checklist and what each command proves.
