# Benchmark: ghostchrome vs Playwright-MCP (cold spawn)

Per-site median over N trials, **cold spawn**: every command starts a fresh process and Chrome session. Snapshot payload = the text content an LLM agent receives from the tool call.

| Site | ghostchrome bytes | pw-mcp bytes | tokens ratio | ghostchrome ms | pw-mcp ms | latency ratio |
|---|---:|---:|---:|---:|---:|---:|
| dashboard | 2440 | 11016 | **4.51×** | 1355 | 1200 | **0.89×** |
| example | 112 | 488 | **4.36×** | 1360 | 1220 | **0.90×** |
| github | 3996 | 13099 | **3.28×** | 2295 | 2090 | **0.91×** |
| hn | 13663 | 58256 | **4.26×** | 2230 | 2110 | **0.95×** |
| landing | 1628 | 5688 | **3.49×** | 1370 | 1260 | **0.92×** |
| news | 3651 | 8998 | **2.46×** | 1400 | 1215 | **0.87×** |
| product | 1743 | 5823 | **3.34×** | 1345 | 1205 | **0.90×** |
| search | 5048 | 9681 | **1.92×** | 1345 | 1255 | **0.93×** |
| **Overall** | 32281 | 113049 | **3.50×** | 12700 | 11555 | **0.91×** |

_Lower is better for ghostchrome columns. Ratio = pw-mcp / ghostchrome (so 3.0× means ghostchrome returns 3× less / runs 3× faster)._

> **Latency caveat:** cold-spawn mode times the full Chrome+process startup on every call. For the real LLM-agent loop (long-lived `serve` instance), see `tools/benchmark/results-warm.md`: run `BENCH_MODE=warm ./tools/benchmark/run-bench.sh` to regenerate.

## Binary size

- ghostchrome: 24.6 MB (single Go binary)
- Playwright-MCP: requires Node.js (~80 MB) + Playwright (~250 MB w/ browsers)
