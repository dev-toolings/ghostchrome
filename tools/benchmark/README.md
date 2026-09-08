# Benchmark Harness

This folder contains a reproducible benchmark harness to compare browser stacks for LLM agents:

- `ghostchrome`
- `Playwright`

and agent CLIs:

- `Codex`
- `Claude Code`

The benchmark is task-based. It is designed to answer the only comparison that matters:

`tokens per successful task`

instead of only `tokens per snapshot`.

## What To Measure

For each run, record:

- `success`
- `duration_ms`
- `tool_calls`
- `input_tokens` and `output_tokens`

If exact token usage is unavailable, record:

- `input_chars`
- `output_chars`

The harness will estimate tokens as `ceil(chars / 4)` and mark those runs as estimated.

### Playwright CLI mode

To compare against the Playwright CLI binary, use:

```bash
./tools/benchmark/run-bench-playwright-cli.sh
```

For session-reuse runs (approximate real-agent loop):

```bash
BENCH_MODE=warm ./tools/benchmark/run-bench-playwright-cli.sh
```

For reproducible runs, pin the Playwright CLI package instead of relying on
`latest`:

```bash
PWCLI_PACKAGE=@playwright/cli@<version> TRIALS=7 SKIP_PUBLIC=1 \
  ./tools/benchmark/run-bench-playwright-cli.sh

PWCLI_PACKAGE=@playwright/cli@<version> BENCH_MODE=warm TRIALS=7 SKIP_PUBLIC=1 \
  ./tools/benchmark/run-bench-playwright-cli.sh
```

Note: the benchmark measures the bytes/latency of the `open` (cold) / `goto` (warm) command only. In Playwright CLI, these commands already include an automatic snapshot in their normal output, so no explicit `snapshot` command is executed in the harness.

Benchmark outputs are written to:

- `tools/benchmark/results-cli.md` / `tools/benchmark/results-cli.json`
- `tools/benchmark/results-cli-warm.md` / `tools/benchmark/results-cli-warm.json`
- `tools/benchmark/badges-cli/tokens.json`
- `tools/benchmark/badges-cli/latency.json`

### Concrete Playwright CLI benchmark protocol

Use the official Playwright Agent CLI documentation as the command source:

- <https://playwright.dev/agent-cli/introduction>
- <https://playwright.dev/agent-cli/capabilities>

Run command-surface checks first, so the benchmark is not comparing invented or
undocumented names:

```bash
GOCACHE=/tmp/go-build go test ./cmd \
  -run 'TestPlaywrightCompatCommandsRegistered|TestPlaywrightCompatFlagsRegistered'
```

Then run the same site set in both modes:

```bash
PWCLI_PACKAGE=@playwright/cli@<version> TRIALS=7 SKIP_PUBLIC=1 \
  ./tools/benchmark/run-bench-playwright-cli.sh

PWCLI_PACKAGE=@playwright/cli@<version> BENCH_MODE=warm TRIALS=7 SKIP_PUBLIC=1 \
  ./tools/benchmark/run-bench-playwright-cli.sh
```

Keep these invariants fixed for every published comparison:

- same machine, CPU governor, OS, and Chrome installation
- same `PWCLI_PACKAGE`
- same `TRIALS`, `BENCH_SITES`, `SKIP_PUBLIC`, and fixture files
- cold and warm results reported separately
- no extra Playwright `snapshot` command after `open` / `goto`
- median values reported, with raw JSON kept for audit

Treat the microbenchmark as a payload/latency/RSS comparison. For serious
agent-level claims, use the suite JSON format below and record task success,
tool calls, and token usage per task.

## Suite Format

Use one JSON file containing:

- suite metadata
- task definitions
- all runs

See [sample-suite.json](sample-suite.json).

Each run should represent one attempt of one task with one pairing:

- `agent`: `codex` or `claude-code`
- `browser`: `ghostchrome` or `playwright`

Example run:

```json
{
  "task_id": "find-price",
  "agent": "codex",
  "browser": "ghostchrome",
  "success": true,
  "duration_ms": 3200,
  "tool_calls": 4,
  "input_tokens": 1300,
  "output_tokens": 250
}
```

## Recommended Protocol

Use the same machine, network, and target URLs for every run.

Run each task at least 5 times for each pairing:

- `codex + ghostchrome`
- `codex + playwright`
- `claude-code + ghostchrome`
- `claude-code + playwright`

Keep the task prompts identical across pairings.

For Playwright CLI mode, pairings should use the same site fixture set and the same
`TRIALS`, `BENCH_SITES`, and fixture server if used.

Recommended task groups:

1. Inspection
2. Ecommerce navigation
3. Interaction

Recommended tasks:

1. Open the home page and list the main interactive elements.
2. Detect console and network errors after load.
3. Find a product and return price + URL.
4. Dismiss the cookie banner and re-extract the useful DOM.
5. Add one product to cart and report the final counter.

## Run The Comparator

```bash
go run ./tools/benchmark/cmd/benchcmp --input tools/benchmark/sample-suite.json
```

Write Markdown and JSON outputs:

```bash
go run ./tools/benchmark/cmd/benchcmp \
  --input tools/benchmark/my-suite.json \
  --markdown-out tools/benchmark/latest-report.md \
  --json-out tools/benchmark/latest-report.json
```

## Score Model

The default composite score is:

- `50%` success rate
- `25%` tokens per success
- `15%` median duration
- `10%` median tool calls

The report gives you:

- overall scoreboard
- winner by agent
- per-task comparison

## How To Collect Tokens

Preferred order:

1. Exact usage reported by the agent runtime or API.
2. Exact usage exported from CLI logs or traces.
3. Character-based estimate if exact usage is unavailable.

Do not mix measurement methods across pairings unless you have to. If one agent only gives estimates, record that in the run notes and keep the comparison honest.
