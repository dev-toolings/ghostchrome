# Latency per Chrome connection mode (2026-10-09)

`tools/benchmark/cdp_modes.py` runs one agent loop against
`fixtures/product.html`: navigate + snapshot, `eval` of `#qty`, and a click on
a button that does not navigate. CLI modes spawn one process per step, MCP
modes send the steps to one persistent stdio server. Values are milliseconds,
median of the measured loops after two discarded warm-up loops; "first loop"
includes the cold start (daemon spawn, MCP initialize, container start).

## GitHub Actions `ubuntu-latest`, Chrome stable (CI run 37945358834, 10 loops)

| Mode | First loop | Loop median | Loop p95 | navigate+snapshot | eval | click |
|---|---:|---:|---:|---:|---:|---:|
| cli-daemon | 1872 | 283 | 289 | 64 | 21 | 197 |
| cli-attach | 475 | 325 | 439 | 91 | 34 | 203 |
| mcp-local | 568 | 116 | 119 | 42 | 1 | 73 |
| mcp-connect | 186 | 117 | 118 | 45 | 3 | 68 |
| docker-cdp | 830 | 845 | 960 | 338 | 159 | 364 |
| docker-cdp-hostnet | 663 | 327 | 433 | 92 | 34 | 202 |
| docker-exec | 890 | 434 | 456 | 121 | 72 | 241 |
| docker-mcp | 721 | 118 | 183 | 46 | 2 | 72 |

| Launcher (`--version`) | median | p95 |
|---|---:|---:|
| binary | 8 | 8 |
| sdk/launcher (bun) | 41 | 48 |
| bunx github: | 51 | 55 |

## Linux workstation, Chromium 128 from Rod's cache (15 loops)

| Mode | First loop | Loop median | Loop p95 | navigate+snapshot | eval | click |
|---|---:|---:|---:|---:|---:|---:|
| cli-daemon | 1003 | 293 | 306 | 74 | 20 | 199 |
| cli-cold (5 loops) | 5353 | 4958 | 5333 | 1635 | 1628 | 1696 |
| cli-connect | 397 | 336 | 363 | 101 | 32 | 205 |
| cli-attach | 297 | 342 | 417 | 101 | 35 | 213 |
| mcp-local | 782 | 117 | 138 | 45 | 1 | 70 |
| mcp-connect | 188 | 164 | 174 | 65 | 10 | 86 |

| Launcher (`--version`) | median | p95 |
|---|---:|---:|
| binary | 13 | 16 |
| sdk/launcher (bun) | 48 | 51 |
| bunx github: | 56 | 73 |

## Reading

- **MCP is the fastest loop, wherever Chrome runs** (about 117 ms): one
  process, one CDP connection, no per-step startup. `docker run -i ... mcp`
  costs nothing per step compared with a local server; only its first loop
  pays the container start.
- **The CLI pays a process and a CDP handshake per step** (about 290-340 ms per
  loop). The implicit daemon is the fastest CLI mode; `--connect` and `attach`
  add about 40 ms per loop for the endpoint lookup and attach.
- **Without the daemon the CLI is 17x slower** (about 5 s per loop): every
  command starts Chrome and must navigate again.
- **Published-port CDP into a container is the slow path**: 845 ms per loop,
  2.6x the same container on the host network (327 ms, equal to a local
  attach). Every CDP message crosses `docker-proxy` and the `socat` relay.
  Prefer `docker run -i <image> mcp`, `docker exec`, or `--network host` with
  `CDP_RELAY=0`.
- **The GitHub launcher adds about 40 ms per start** (Bun startup plus the
  cached-binary check). An MCP server starts once, so this cost is paid once
  per session.

Reproduce:

```bash
go build -o .local/bin/ghostchrome ./cmd/ghostchrome
python3 tools/benchmark/cdp_modes.py --bin .local/bin/ghostchrome \
  --chrome "$(command -v google-chrome)" --modes cli-daemon,cli-attach,mcp-local,mcp-connect,launcher
# Docker modes need an image: add --image <tag> and docker-cdp,docker-cdp-hostnet,docker-exec,docker-mcp
```
