# ghostchrome — docs

Ultra-light browser automation **and** HTTP-first fast-path for LLM agents. Single Go binary, zero Node, single static dependency on Chrome (only when actually needed).

## What's in here

| Doc | When to read |
|---|---|
| [`architecture.md`](./architecture.md) | Mental model in one diagram. Read this first. |
| [`cli.md`](./cli.md) | Full command reference grouped by category. |
| [`mcp.md`](./mcp.md) | The 11-tool MCP stdio server — what Claude Code, Codex, Cursor see. |
| [`fast-path.md`](./fast-path.md) | `fastfetch` + `fetchapi` — sub-second scraping without Chrome. **Most users want this.** |
| [`chrome-path.md`](./chrome-path.md) | Browser commands, locator auto-wait, observer, agent JSONL loop, recovery hooks. |
| [`anti-bot.md`](./anti-bot.md) | DataDome / Cloudflare detection, fallback strategy, what works and what doesn't. |
| [`recipes/`](./recipes) | Site-by-site working pipelines (autoscout24, bulk autosphere, Algolia, agent JSONL). |

## 30-second pitch

```
                        ┌─────────────────────────────────┐
                        │   ghostchrome <command> <url>   │
                        └────────────────┬────────────────┘
                                         │
              ┌──────────────────────────┼──────────────────────────┐
              ▼                          ▼                          ▼
       ┌────────────┐            ┌────────────┐            ┌──────────────┐
       │ fastfetch  │            │ fetchapi   │            │ Chrome path  │
       │ HTML SSR   │            │ JSON / XHR │            │ (Rod + CDP)  │
       │ 0 Chrome   │            │ 0 Chrome   │            │ headless     │
       └─────┬──────┘            └─────┬──────┘            └──────┬───────┘
             │                         │                          │
       ~150-400 ms             ~80-150 ms (Algolia)           ~3-5 s/page
       SSR payloads:          generic GET/POST                 Locators auto-wait
       Next, Nuxt, Apollo,    AlgoliaQuery preset              Observer NDJSON
       Initial-State, RSC,    custom headers/body              Agent JSONL loop
       JSON-LD                                                 Recovery hooks
```

When the HTTP path is blocked (DataDome / Cloudflare / no SSR data), every recipe and `fastfetch --fallback-browser` automatically spawn Chrome. Best of both worlds, no manual switch.

## Killer measurement

```
autosphere.fr stock: 15 128 véhicules récupérés en 21,8 s.
Throughput: 693 vehicles/s. Coût: 1,44 ms par véhicule. 0 Chrome.
```

675 pages (`?from=N` offset) parallélisées avec `xargs -P 20`. Voir [`recipes/bulk-scrape.md`](./recipes/bulk-scrape.md).

## Install / build

```bash
go install github.com/MakFly/ghostchrome@latest
# or build from source:
go build -o ghostchrome .
```

## Conventions

- Code/commits in English, conventional commits.
- Search via `ig` (trigram-indexed), never grep/rg.
- After writing/editing files: `ig index .`
- HTTP path is preferred. Chrome is the fallback, not the default.
- `--connect=auto` reuses an already-running Chrome (zero spawn cost).
