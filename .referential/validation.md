# Validation Guide

Use the smallest check that proves the change, then broaden when the touched
surface is shared.

## Core Commands

| Command | What it proves |
|---|---|
| `go build ./...` | All Go packages compile. |
| `go test -short ./...` | Short Go suite without integration tests. |
| `go test ./internal/core/engine/...` | Engine-focused tests from project docs. |
| `go test ./internal/ops/...` | Catalog self-consistency and generated-file freshness. |
| `go generate ./internal/ops/...` | Regenerates the contract and the JSONL/MCP/AI registrations. |
| `just test` | Project shortcut for `go test -short ./...`. |
| `just contract` | Project shortcut for command contract generation. |
| `just test-all` | Go short tests plus both SDK hermetic suites. |

## SDK Commands

```bash
cd sdk/typescript && bun install && bunx tsc --noEmit && bun test
cd sdk/python && python3 -m unittest discover -s tests -q
```

Use these when `contracts/commands.json`, SDK wrappers, SDK types, or JSONL
agent behavior changes.

## Live Shape Measurement

Run this before changing SDK result types or docs that claim JSONL result
shapes:

```bash
google-chrome --headless=new --remote-debugging-port=9222 --user-data-dir=/tmp/gc-measure about:blank &
scripts/measure-agent-ops.sh
```

The measured output is ground truth for runtime result shapes.

## End-To-End Smoke

Requires a running Chrome on port 9222:

```bash
go build -o .local/bin/ghostchrome ./cmd/ghostchrome
GHOSTCHROME_BIN="$PWD/.local/bin/ghostchrome" bun run sdk/examples/typescript/chronovet.ts https://www.chronovet.fr/
GHOSTCHROME_BIN="$PWD/.local/bin/ghostchrome" python3 sdk/examples/python/chronovet.py https://www.chronovet.fr/
```

Or use the shortcut:

```bash
just e2e
```

Do not start a local dev server unless the user explicitly asks for it.

## Practical Validation Matrix

| Change type | Minimum validation |
|---|---|
| README or docs only | Read rendered markdown or inspect diff. |
| One CLI command | `go test -short ./...` or targeted package tests, plus `go build ./...`. |
| Engine behavior | Targeted `go test` for the touched engine area, then `go test -short ./...`. |
| Op catalog/surfaces | `go generate ./internal/ops/...`, `go test ./internal/ops/...`, SDK coverage tests. |
| JSONL result shapes | Build binary, run `scripts/measure-agent-ops.sh`, update SDK tests. |
| TypeScript SDK | `cd sdk/typescript && bun install && bunx tsc --noEmit && bun test`. |
| Python SDK | `cd sdk/python && python3 -m unittest discover -s tests -q`. |
| Cross-surface behavior | `just test-all`, then targeted e2e if browser-visible. |


## CI and release checks

The CI workflow runs on branch pushes, release tags, pull requests to main, and
manual dispatch. It includes Linux/macOS race tests, lint, the 10k-operation
conformance loop, Linux integration, macOS/Windows smoke tests, and both SDKs.
Match the Go version in `go.mod` and the Bun/linter versions in
`.github/workflows/ci.yml` when reproducing a CI failure.

The security workflow uses `bun install --frozen-lockfile --ignore-scripts`
and `bun audit --audit-level=high`. It must fail on installation or audit
errors. Run it when changing workspace manifests, the lockfile, or its workflow.
Check the branch runs before creating a release tag, then verify the tag's CI,
security, and release runs. A successful build alone does not prove CI passed.
