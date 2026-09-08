# Recipe — bulk scrape autosphere (15 128 vehicles in 21,8 s)

Demonstrates the full HTTP-direct + RSC parser pipeline at scale. Real measurement, no Chrome.

## Target

`autosphere.fr` ships a Next.js App Router SERP at `/recherche` whose listings are embedded in an RSC chunk (`__next_f.push([1, "<id>:<json>"])`). The largest chunk (typically `id="7"`) holds the Elasticsearch payload `{ total: 15514, results: [...] }`.

## Pagination

Offset-based via `?from=N`. Page size is 23. URLs:

```
https://www.autosphere.fr/recherche?from=0
https://www.autosphere.fr/recherche?from=23
https://www.autosphere.fr/recherche?from=46
…
https://www.autosphere.fr/recherche?from=15490
```

## One-shot script

```bash
TOTAL=15514
PER_PAGE=23
PAGES=$(( (TOTAL + PER_PAGE - 1) / PER_PAGE ))     # 675

mkdir -p /tmp/asph
seq 0 $PER_PAGE $(( TOTAL - 1 )) \
  | awk '{print "https://www.autosphere.fr/recherche?from=" $1}' \
  > /tmp/asph/urls.txt

S=$(date +%s%3N)
cat /tmp/asph/urls.txt | xargs -P 20 -I{} bash -c '
  URL="$1"
  IDX=$(echo "$URL" | grep -oP "from=\K[0-9]+")
  ghostchrome fastfetch --include-payloads --timeout-ms 10000 "$URL" 2>/dev/null \
    | jq -c "(.ssr_payloads[] | select(.source==\"rsc\" and .type==\"7\") | .data | fromjson? // .) // empty" \
    | jq -c ".. | objects | select(has(\"results\") and has(\"total\")) | .results[]._params._id // empty" \
    > /tmp/asph/p_${IDX}.txt 2>/dev/null
' _ {}
E=$(date +%s%3N)
WALL=$((E-S))

COUNT=$(cat /tmp/asph/p_*.txt 2>/dev/null | grep -v '^$' | tr -d '"' | sort -u | wc -l)

echo "wall=${WALL}ms unique=$COUNT"
rm -rf /tmp/asph     # always clean up scraped data
```

## Measured

| Metric | Value |
|---|---|
| Pages requested | 675 |
| Concurrency | 20 (xargs `-P 20`) |
| Wall time | **21,8 s** |
| Unique vehicle IDs | **15 128** |
| Cost per vehicle | **1,44 ms** |
| Throughput | **693 vehicles/s** |
| Chrome processes spawned | 0 |
| Disk usage | < 1 MB |

The drift between advertised total (15 514) and unique IDs (15 128) is normal — stock changes between requests (sold / new listings).

## Why this works

1. `fastfetch --include-payloads` returns the full RSC payload set in the JSON envelope.
2. `engine.ExtractRSCPayloads` decodes `self.__next_f.push([1, "<id>:<value>\n…"])` chunks back into `(id, value)` tuples, JSON-decoding the outer string escape.
3. Chunk `id="7"` is consistently the listings island (highest-volume JSON tuple in autosphere's stream).
4. The data lives at `.. | objects | select(has("results"))` — a recursive jq walk handles the RSC element-tree wrapper `[$, $L1b, null, { initialValues: { results: [...] } }]`.

## Comparison

| Approach | Wall time |
|---|---|
| **HTTP fast-path × 20 parallel** | **21,8 s** |
| Chrome headless sequential @ ~3,5 s/page | ~2 363 s (~40 min) |
| Chrome headless × 20 parallel | likely ~2 min + memory pressure (Chrome × 20 = ~6 GB RAM) |

**108× faster** than sequential Chrome; **~5-6× faster** than parallel Chrome with a fraction of the resource cost.

## Cleanup discipline

Always remove scraped data after measurement. Don't keep it cached in `/tmp` between runs. The example above ends with `rm -rf /tmp/asph` — this is the rule, not an option, for any third-party scraping work.

## Adapting to other sites

The pattern generalizes to any Next.js App Router site:

1. Open the SERP in DevTools → Network → check the document response for `__next_f.push`.
2. `ghostchrome fastfetch --include-payloads <url> | jq '.ssr_payloads[] | select(.source=="rsc")'` — find the chunk holding the listings.
3. Identify the `<id>` for the listings chunk (often consistent across pages).
4. Build the URL list (look for query params in the SERP).
5. Parallelize with `xargs -P` or a Go goroutine pool.

If the site uses Algolia or a JSON API instead, `fetchapi algolia` or `fetchapi` is even faster (sub-100 ms per page typically).
