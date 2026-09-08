# Real benchmark: ghostchrome vs Playwright CLI and MCP

Run date: 2026-08-25 (UTC)

This report supersedes the older headline benchmark for the versions and
protocol below. It measures effective agent-consumed output, including the YAML
snapshot file linked by current Playwright CLI/MCP responses.

## Environment and protocol

- Host: Linux 7.0, AMD Ryzen 7 3700X (8 cores / 16 threads), 30 GiB RAM
- Browser for every pairing: Chromium 128.0.6568.0, same executable
- ghostchrome: current checkout (`version dev`, built immediately before run)
- Playwright CLI: `@playwright/cli@0.1.18`
- Playwright MCP: `@playwright/mcp@0.0.79`
- Workload: five deterministic local HTTP pages (`landing`, `product`,
  `search`, `dashboard`, `news`)
- Seven measured warm-session trials per page and pairing; one warm-up was
  discarded; site order was rotated between trials
- Every sample was accepted only when its effective payload contained a known
  expected value from the page
- Token counts are estimates (`ceil(UTF-8 bytes / 4)`), not model tokenizer
  measurements
- Timings are end-to-end wall time at the CLI or MCP boundary. Reading a local
  Playwright snapshot file is included in payload size and agent-read count,
  but its sub-millisecond filesystem time is not added to browser call latency.

## Inspection task: CLI

ghostchrome used one warm `preview URL --wait load` command. Playwright used
one warm `goto URL`, followed by reading the linked YAML snapshot. Values are
per-site medians over seven trials.

| Site | ghostchrome bytes | Playwright bytes | ghostchrome ms | Playwright ms | Verified |
|---|---:|---:|---:|---:|---:|
| landing | 2,673 | 5,983 | 54 | 678 | 7/7 + 7/7 |
| product | 2,774 | 6,239 | 56 | 691 | 7/7 + 7/7 |
| search | 7,055 | 9,732 | 69 | 673 | 7/7 + 7/7 |
| dashboard | 4,111 | 10,264 | 63 | 664 | 7/7 + 7/7 |
| news | 5,169 | 8,816 | 56 | 671 | 7/7 + 7/7 |
| **Five-site total** | **21,782** | **41,034** | **298** | **3,377** | **35/35 + 35/35** |

Result: ghostchrome returned **1.88x fewer effective bytes** (about 5,446 vs
10,259 estimated tokens) and completed the five-page set **11.3x faster**.
The normal inspection loop requires one agent read per page with ghostchrome
and two with the current Playwright file-backed snapshot response.

## Inspection task: MCP

ghostchrome used one `snapshot({url, wait: "load", level: "content"})` call.
Playwright used one `browser_navigate({url})` call and the linked YAML snapshot.
No redundant `browser_snapshot` call was made.

| Site | ghostchrome bytes | Playwright bytes | ghostchrome ms | Playwright ms | Verified |
|---|---:|---:|---:|---:|---:|
| landing | 11,803 | 5,980 | 39 | 57 | 7/7 + 7/7 |
| product | 11,748 | 6,234 | 50 | 64 | 7/7 + 7/7 |
| search | 23,562 | 9,727 | 62 | 80 | 7/7 + 7/7 |
| dashboard | 17,977 | 10,261 | 49 | 62 | 7/7 + 7/7 |
| news | 19,081 | 8,813 | 42 | 54 | 7/7 + 7/7 |
| **Five-site total** | **84,171** | **41,015** | **242** | **317** | **35/35 + 35/35** |

Result: ghostchrome was **1.31x faster**, but its current MCP snapshot was
**2.05x larger** (about 21,043 vs 10,254 estimated tokens). This reverses the
old repository headline for current versions. On the product fixture alone,
the ghostchrome MCP JSON spends 7,487 bytes on `dom.nodes` and another 3,148
bytes on `dom.refs`; that duplication is the clearest optimization target.

MCP discovery has the opposite result:

| Metric | ghostchrome | Playwright | Ratio |
|---|---:|---:|---:|
| Tools | 16 | 24 | Playwright 1.50x more |
| Serialized `tools/list` schemas | 9,000 B | 18,502 B | Playwright 2.06x larger |
| Initialize response time | 10 ms | 302 ms | Playwright 30.2x slower |

The schema byte count is a transport-level lower bound. MCP hosts may serialize
tool definitions differently in the model prompt. Initialization does not mean
the browser is ready; the warm-up call was discarded for the task timings.

Process-tree RSS after navigation was about 0.85 GiB for ghostchrome and 0.97
GiB for Playwright (roughly 15% higher). This is indicative only: summed Linux
RSS double-counts shared pages and is not a portable memory metric.

## Verified interaction task

Task: navigate to the product fixture, locate the `Quantity` combobox from the
snapshot, select `3`, evaluate `#qty.value`, and require the result to equal
`3`. Medians are over seven warm trials.

| Surface | Tool | Success | Agent calls/reads | Effective bytes | Est. tokens | Duration |
|---|---|---:|---:|---:|---:|---:|
| CLI | ghostchrome | 7/7 | 3 | 3,468 | 867 | 443 ms |
| CLI | Playwright | 7/7 | 4 | 6,515 | 1,629 | 2,459 ms |
| MCP | ghostchrome | 7/7 | 3 | 11,770 | 2,943 | 408 ms |
| MCP | Playwright | 7/7 | 4 | 6,542 | 1,636 | 634 ms |

For this interaction, ghostchrome CLI was **5.55x faster** with **1.88x fewer
bytes**. ghostchrome MCP was **1.55x faster**, but used **1.80x more bytes**.

## Practical conclusion

- For coding agents that can execute shell commands, ghostchrome CLI is the
  clear winner in this run: compact direct output, fewer reads, and materially
  lower warm command latency.
- For MCP, ghostchrome currently wins latency and schema overhead, but loses
  snapshot payload size. It should not claim fewer MCP snapshot tokens against
  current Playwright until the duplicated structured DOM/ref output is reduced.
- Playwright remains the broader runtime: Firefox/WebKit support, native
  Playwright tracing and `run-code` are real capability advantages that this
  browser-inspection benchmark does not score.
- The benchmark measures deterministic browser/tool execution, not an LLM's
  reasoning quality or exact billed tokens. A model-in-the-loop study would be
  a separate, more expensive experiment with prompt and model variance.

Reproduction helpers: `tools/benchmark/cli-measure.mjs` and
`tools/benchmark/mcp-measure.mjs`. They emit one JSON record per raw sample.
