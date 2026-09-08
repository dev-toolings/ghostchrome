# Benchmark : ghostchrome vs Playwright-MCP (warm session)

Per-site median over N trials, **warm session**: ghostchrome runs against a long-lived `serve` instance, Playwright-MCP keeps one MCP server alive for all URLs. Each timing measures only `navigate + snapshot`, not process startup. This is the real LLM-agent loop.

| Site | ghostchrome bytes | pw-mcp bytes | tokens ratio | ghostchrome ms | pw-mcp ms | latency ratio |
|---|---:|---:|---:|---:|---:|---:|
| dashboard | 2193 | 10984 | **5.01×** | 50 | 64 | **1.28×** |
| example | 112 | 443 | **3.96×** | 70 | 120 | **1.71×** |
| hn | 13663 | 58256 | **4.26×** | 660 | 1023 | **1.55×** |
| landing | 1504 | 5688 | **3.78×** | 95 | 274 | **2.88×** |
| news | 3404 | 8966 | **2.63×** | 40 | 51 | **1.27×** |
| product | 1557 | 5823 | **3.74×** | 45 | 55 | **1.22×** |
| search | 4893 | 9681 | **1.98×** | 60 | 73 | **1.22×** |
| **Overall** | 27326 | 99841 | **3.65×** | 1020 | 1660 | **1.63×** |

_Lower is better for ghostchrome columns. Ratio = pw-mcp / ghostchrome (so 3.0× means ghostchrome returns 3× less / runs 3× faster)._

## Binary size

- ghostchrome: 24.6 MB (single Go binary)
- Playwright-MCP: requires Node.js (~80 MB) + Playwright (~250 MB w/ browsers)
