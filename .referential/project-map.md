# Project Map

## Product Shape

`ghostchrome` is a single Go binary for compact browser automation. The target
user is an LLM agent or automation script that needs to inspect a page, receive
small output with stable refs such as `@1`, and then interact through follow-up
commands.

The project optimizes for:

- Small token output from filtered accessibility extraction.
- Fast warm loops through persistent Chrome attachment.
- A CLI-first interface, with MCP and SDK surfaces layered on top.
- No Node runtime dependency for the core binary.

## Top-Level Directories

| Path | Role |
|---|---|
| `cmd/` | Main packages only: `ghostchrome/` and `ghostchrome-mcp/`. |
| `skillbundle.go` | Repo-root package owning the `//go:embed` of the agent skill. |
| `internal/surface/cli/` | Cobra commands. One file per command. Keep command files thin. |
| `internal/core/engine/` | Shared browser, CDP, extraction, interaction, network, and state logic. |
| `internal/setup/` | Installation, transports, embedded skill bundle, diagnostics. |
| `internal/compat/playwright/` | Playwright CLI config schema and value rules. |
| `internal/ops/` | Canonical operation catalog; `internal/ops/gen` renders every surface registration from it. |
| `contracts/` | Generated command/op contract consumed by SDK coverage tests. |
| `docs/` | Human documentation for architecture, CLI, MCP, anti-bot, fast path, recipes. |
| `sdk/typescript/` | TypeScript JSONL agent client, Bun-based tests/build. |
| `sdk/python/` | Python JSONL agent client, stdlib runtime and unittest tests. |
| `examples/` | End-to-end SDK examples. |
| `benchmark/` | Benchmark fixtures, runner, and result docs. |
| `packages/` | Site-specific scraper packages and npm wrapper package. |
| `.claude/skills/ghostchrome/` | Embedded agent skill bundled into the binary. |

## Engine Responsibilities

The main `internal/core/engine/` split is:

- Browser lifecycle: `internal/core/engine/browser.go`, session/context/profile helpers.
- Navigation and waiting: `internal/core/engine/navigator.go`, `internal/core/engine/wait.go`,
  `internal/core/engine/locator_wait.go`.
- Extraction: `internal/core/engine/extractor.go`, DOM fallback, SSR/RSC extractors.
- Interaction: `internal/core/engine/interactor.go`, drag, clipboard, mouse, human mode.
- Observation: `internal/core/engine/errors.go`, `internal/core/engine/observer.go`, network tracker,
  capture, HAR, intercept.
- Page reports: `internal/core/engine/preview.go`, perf, screenshots, PDF, annotations.
- Safety and stealth: `internal/core/policy/`, `internal/core/vault/`, `internal/core/engine/stealth.go`,
  anti-bot blocker.
- Protocol surfaces: `internal/surface/mcp/`, `internal/surface/ai/`.

## Command Groups

The Cobra root groups commands into these families:

| Group | Examples |
|---|---|
| Session | `serve`, `login`, `import-profile`, `extensions` |
| Navigate | `navigate`, `back`, `forward`, `scroll`, `viewport`, `tabs` |
| Observe | `extract`, `preview`, `eval`, `errors`, `capture`, `screenshot`, `perf` |
| Interact | `click`, `type`, `press`, `hover`, `select`, `fill-form`, `upload`, `drag` |
| Wait | `waitfor`, `wait-port`, `wait-url` |
| State | `cookies`, `storage`, `intercept`, `assert`, `batch`, trace commands |
| Utility | `doctor`, `init-script`, `dashboard`, `mcp` |
| Recipes | `linkedin`, `leboncoin`, `autoscout24`, `cars-listings` |

## Operation Surfaces

`internal/ops/ops.go` names three live surfaces:

- `jsonl`: `internal/runtime` dispatch table (`runtime.Ops()`), driven by the
  `internal/surface/cli/agent.go` stdio loop; also the SDK method coverage set.
- `mcp`: `internal/surface/mcp/tools_gen.go` registered MCP tools (`mcp.Tools()`).
- `ai`: `internal/surface/ai/tools_gen.go` provider tool specs (`ai.ToolSpecs()`).

All three registration tables are generated from `ops.Catalog()`; only the
handler bodies are hand-written.

Intentional divergences exist. Do not treat them as drift without checking the
catalog comments:

- `snapshot` is MCP-only because it bundles navigate, extract, and errors.
- `done` is AI-only and has no CDP side effect.
- `wait_for` is MCP-only, while JSONL uses `wait`.
- `fill`, `init`, and `close` are JSONL-only lifecycle/convenience ops.
- `errors` and `url` are JSONL/AI only because MCP covers them through
  `snapshot`.
- `screenshot` is JSONL/MCP, not AI.

## Data Shape Rules

The op catalog defines names, args, summaries, and surface exposure. It does not
define every runtime result shape.

SDK result types must follow live binary measurements. Known examples captured
in current docs:

- `extract.stats` uses `total_nodes`, `filtered_nodes`, and
  `interactive_count`.
- `errors` returns a top-level array.
- Empty-result ops omit `result`.

