# Fast path — `fastfetch` & `fetchapi`

The HTTP-first lane. Sub-second on most pages. Zero Chrome until proven necessary.

## When to reach for it

| Question | If yes |
|---|---|
| Does the target render meaningful data on first byte (SSR)? | `fastfetch` |
| Is data fetched via XHR with stable params (Algolia / REST / GraphQL)? | `fetchapi` |
| Are you rendering JS, animations, or interacting? | Chrome path |

A good heuristic: open DevTools → Network → look for the **document** request. If the response HTML already contains your data, `fastfetch` will read it. If the data only appears after XHR, capture the URL+headers and replay with `fetchapi`.

## `fastfetch` — what it actually does

1. Single GET with realistic browser headers (`User-Agent`, `Accept`, `Accept-Language`, `Sec-Fetch-*`, gzip).
2. Walks the response body for every supported SSR data island.
3. Detects anti-bot challenges by status + body markers.
4. Returns one JSON envelope summarizing everything.

### Supported SSR sources

| Source | Marker | Format |
|---|---|---|
| `next` | `<script id="__NEXT_DATA__" type="application/json">` | strict JSON |
| `nuxt` | `<script id="__NUXT_DATA__" ...>` (Nuxt 3) | strict JSON |
| `nuxt-js` | `window.__NUXT__=(function(){…}())` (Nuxt 2) | JS expression (raw string) |
| `apollo` | `window.__APOLLO_STATE__=…` | usually JSON |
| `initial-state` | `window.__INITIAL_STATE__=…` | usually JSON |
| `preloaded` | `window.__PRELOADED_STATE__=…` | usually JSON |
| `redux` | `window.__REDUX_STATE__=…` | usually JSON |
| `jsonld` | `<script type="application/ld+json">` | strict JSON, classified by `@type` |
| `rsc` | `self.__next_f.push([1, "<id>:<value>\n…"])` (App Router) | tuples; values are JSON / module refs / text |

Each payload comes with `is_json: true|false` so you know whether to `json.Unmarshal` or treat as raw string.

JSON-LD blocks are extra-classified by `@type` (handles `@graph` wrappers): `Product`, `Vehicle`, `RealEstateListing`, `Offer`, `BreadcrumbList`, `Organization`, `WebSite`, `AutoDealer`, `ListItem`, `FAQPage`, `Person`, …

### Anti-bot detection

Conservative — false positives cost a Chrome fallback for nothing.

| Trigger | Verdict |
|---|---|
| HTTP `403` / `429` / `503` | blocked |
| `geo.captcha-delivery.com` + body < 30 KB + `you have been blocked` / `interstitial` / `<title>access denied` | blocked (DataDome) |
| `cf-browser-verification` / `cf_chl_opt` / `<title>just a moment` + `challenge-platform` | blocked (Cloudflare) |
| `<title>captcha` | blocked |
| DataDome SDK loaded on a normal page | **not** blocked (no false positive) |

### Examples

```bash
# Probe a target
ghostchrome fastfetch https://www.autoscout24.fr/lst/audi/a3?fregfrom=2020 \
  | jq '{status, mode, has_next_data, ssr_sources, json_ld_types, elapsed_ms}'

# Pipe directly to a jq pipeline
ghostchrome fastfetch --next-data 'https://www.autoscout24.fr/lst/audi/a3' \
  | jq '.props.pageProps.listings[] | {price: .price.priceFormatted, url}'

# Cookie auth + force browser if blocked
ghostchrome fastfetch --header 'Cookie=session=…' --fallback-browser https://gated.fr

# Save the raw HTML
ghostchrome fastfetch --raw -o page.html https://example.com

# Inspect every SSR island in detail
ghostchrome fastfetch --include-payloads --pretty https://www.autosphere.fr/recherche \
  | jq '.ssr_payloads[] | {source, type, size: (.data | tostring | length)}'
```

## `fetchapi` — XHR replay

When the data lives behind an API call rather than in the HTML, `fetchapi` does that one call with whatever headers/body you provide.

### Generic

```bash
ghostchrome fetchapi --method POST \
  --header 'Content-Type=application/json' \
  --header 'Authorization=Bearer eyJ…' \
  --data '{"query":"audi a3"}' \
  --body-only \
  https://api.example.com/search | jq .
```

### Algolia preset

Several recipes ship Algolia config in `sites.json` (capcar, starterre, …). Replay them in one call:

```bash
ghostchrome fetchapi algolia \
  --app-id 691K8M71IA \
  --api-key 95874bf3cc96f8de61eced3440501724 \
  --index production_cars \
  --params 'query=audi+a3&hitsPerPage=20&page=0' \
  --body-only | jq '{nbHits, sample: .hits[0]}'
```

Result envelope (without `--body-only`):

```json
{
  "status": 200,
  "url": "https://...",
  "headers": { ... },
  "is_json": true,
  "json": { "hits": [...], "nbHits": 64, "nbPages": 4 },
  "content_length": 31415,
  "elapsed_ms": 96
}
```

## Capturing endpoints to replay

Use Chrome path's observer to discover what to call:

```bash
# 1. Watch what the SERP fetches
ghostchrome watch https://target.fr/search?q=foo --duration 6s --filter-net=xhr,fetch \
  --observe-out events.jsonl

# 2. Find the API call
jq 'select(.kind=="net" and .status==200) | {url, method, type}' events.jsonl

# 3. Replay with fetchapi
ghostchrome fetchapi <url-from-step-2>
```

## Measured

| Site | Endpoint | Throughput |
|---|---|---|
| autoscout24 SERP `/lst/audi/a3` | fastfetch (Next + 3× JSON-LD) | 264 ms / page |
| autoscout24 DETAIL `/offres/...` | fastfetch (Product schema) | 227 ms |
| autosphere `/recherche?from=N` | fastfetch (RSC chunk 7) | 350 ms / page · 21,8 s for 15 128 vehicles @ P=20 |
| capcar Algolia | fetchapi algolia | 96 ms / 64 hits |
| starterre Algolia | fetchapi algolia | 107 ms / 3 463 hits |
| viaautomobile `/eveho/vehicles` | fetchapi GET | 911 ms / 24 vehicles |

Compared to Chrome headless on the same pages: typically **30-100× faster** with identical structured output.

## When fast-path fails

`fastfetch` returns a `blocked` flag and a `reason`. `fetchapi` returns the actual non-2xx status. Fall back to Chrome:

```bash
# fastfetch with auto-fallback (re-extracts via Chrome HTML)
ghostchrome fastfetch --fallback-browser --stealth https://gated.fr

# Or have your recipe do it:
#   recipe.go:
#     res, _ := engine.FastFetch(ctx, url, opts)
#     if res.Blocked || len(res.SSRPayloads) == 0 {
#         spawnChrome()
#         …
#     }
```

See [`anti-bot.md`](./anti-bot.md) for the full fallback strategy.


## Optional Chrome TLS fallback

`fastfetch --fallback-tls <url>` retries a blocked or SSR-empty HTTP GET once
using an in-process Go Chrome 146 TLS/HTTP2 profile. This is the generic equivalent
of the external curl-cffi approach: no Python, external executable, imported
browser cookies, or second browser is required. Its default headers use a coherent
Chrome 146/macOS identity; explicit User-Agent overrides remain caller-owned.

The order is ordinary HTTP, then TLS fallback, then Chrome only when
`--fallback-browser` is also supplied. Useful SSR on the first response skips the
TLS attempt. HTTP 401, 429 and ordinary 404 responses do not trigger it. Both HTTP
attempts share `--timeout-ms`; cancellation closes the request. TLS verifies
certificates and permits at most five same-origin redirects. Failure keeps an
explicit blocked result or error; it never proves stock availability.

The JSON envelope reports `mode: "http-tls"` when the TLS response is selected.
`--raw`, `--next-data` and `--include-payloads` keep their existing formats.
Go consumers can set `engine.FastFetchOpts{FallbackTLS: true}` and inspect
`FastResult.Transport`. This HTTP-only command is not a new MCP or agent operation;
it does not modify installed transport registration or browser navigation.

The implementation is opt-in. No marketplace selectors, GPU rules, Discord
configuration or purchase logic have been moved into Ghostchrome.

A live opt-in test exercises the source code without rebuilding the installed
command:

```sh
GHOSTCHROME_TEST_TLS_URL=https://www.leboncoin.fr/ck/accessoires_informatique/rtx-4090 go test ./engine -run '^TestFastFetchTLSLive$' -v -count=1
```
