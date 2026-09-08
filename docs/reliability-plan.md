# Reliability and performance plan

This plan defines the release bar for the agent loop (CLI, JSONL/SDK, MCP,
navigation, snapshots, interactions, tabs). Recipes, tracing, and WebKit are
outside this gate.

## Target architecture

```text
LLM / SDK / shell
        │ JSONL · MCP · CLI
        ▼
Runtime + PageSession (one serialized action path)
        │ semantic refs · actionability · bounded waits
        ▼
Rod / CDP ── EventHub (navigation, popup, download, dialog, errors)
        ▼
Long-lived Chrome profile / named session
```

The hot path must do one actionability evaluation and one CDP input command.
Extraction is explicit or selected by `snapshot=diff|full`; `snapshot=none`
does not extract. Event-driven watchers handle asynchronous side effects so
ordinary button clicks do not pay popup/tab scans or fixed sleeps.

## Reference alignment

The comparison target is the official [vercel-labs/agent-browser](https://github.com/vercel-labs/agent-browser)
CLI. Its current design uses a native daemon, accessibility snapshots with
refs, persistent named sessions, a profile-based MCP surface, and a
cross-platform release matrix. ghostchrome now matches the first four agent
loop invariants on its Go/Rod stack: one persistent session, compact refs,
bounded event-driven waits, and parity across CLI, JSONL, MCP, and SDKs.

The remaining parity work is deliberately staged after the core release gate:
installer/doctor ergonomics, richer state/network/debug profiles, and a native
macOS/Windows browser smoke run. These are capability gaps, not reasons to add
latency to the click hot path.

## Release gates

| Gate | Acceptance | Evidence |
|---|---|---|
| Warm latency | p95 ≤ agent-browser baseline + 10% on n=100 | `tools/benchmark/results-agent-browser-click.md` |
| Core correctness | ≥99.9% successful operations over 10,000 clicks/diffs | `TestConformanceCoreLoop` |
| Soak | 8 h continuous loop, zero failures | `TestConformanceDuration` |
| Stability | zero deadlocks/crashes during soak and targeted browser tests | test logs + CI |
| Cross-platform | Linux and macOS build/race + browser smoke; Windows build/short smoke | `.github/workflows/ci.yml` |
| CI conformance | 10,000-op core loop is an explicit Linux job on every PR/push | `.github/workflows/ci.yml` (`conformance-10k`) |

The current Linux warm benchmark is 50.5 ms p95 versus 59.3 ms for
agent-browser (gate: 65.3 ms). The 10k loop has passed with 0 failures. A
long-running stress check reached 35,327 operations with 0 failures before it
was intentionally stopped; it is an optional last-resort release check, not a
runtime prerequisite.

At the 29:00-30:00 minute stability sample, the test process stayed at 25
threads and 9 file descriptors (RSS 31.4 MiB); the engine child stayed at 20
threads and 13 descriptors (RSS 45.1→45.7 MiB). This is a short sample, not a
substitute for the full soak leak gate.

A later 37:38-38:44 minute sample held at 20 engine threads and 13 file
descriptors, with RSS 52.4→52.5 MiB; the test process remained at 25 threads,
9 descriptors, and 31.4 MiB RSS.

## Work already in the implementation

- Persistent embedded/named sessions and lease-aware idle reaping.
- Semantic ref recovery (`backendNodeId` then strict role/name/nth), with
  ambiguity and stale refs failing closed.
- Single-evaluation actionability (visibility, hit testing through open shadow
  DOM, conditional scroll) and no fixed settle delay on ordinary clicks.
- Frame-aware extraction and interaction for nested same-origin iframes.
- Event-driven same-tab/SPA navigation, popups, downloads, and reload waits.
- Persistent dialog and file-chooser listeners, including re-arming after
  navigation; dialog policy updates are synchronized and iframe listeners are
  deduplicated by CDP session.
- Browser ownership close now stops all page EventHubs, including direct
  `Browser.Close()` callers, preventing page-keyed listener retention.
- Bounded post-action DOM observation for `snapshot=full`/diff, while keeping
  `snapshot=none` on the hot path.

## Execution order

1. Rerun the optional 8-hour soak only when a release requires the extra stress
   evidence; rerun the 100-click benchmark if any hot-path code changes.
2. Keep macOS browser smoke on pull requests and pushes; execute it on native
   GitHub macOS runners. Treat Linux/macOS as release blockers.
3. Exercise race-sensitive paths (dialog policy updates, event hub retargeting,
   popup/file chooser adoption) under `go test -race`; remove any remaining
   per-page registry retention if long tab rotation demonstrates growth.
4. Add benchmark drift checks to CI and preserve raw samples, not only means;
   investigate any p95 regression over the gate before release.
5. Cut the breaking release only after every gate has direct evidence; publish
   the residual limitations (cross-origin/closed shadow DOM and Playwright
   runtime-only features) explicitly.

## Reproduction commands

```bash
go test -short -count=1 -race ./...
go test ./engine -run 'TestConformanceCoreLoop|TestCaptureMutationWaitsForXHR' \
  -count=1 -timeout 20m
PLAYWRIGHT_CLI_BIN=/bin/false python3 tools/benchmark/click-vs-agent-browser.py 100
GHOSTCHROME_SOAK=1 GHOSTCHROME_SOAK_DURATION=8h \
  go test ./engine -run TestConformanceDuration -count=1 -timeout 9h -v
```
