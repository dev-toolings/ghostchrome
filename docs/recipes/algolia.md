# Recipe — Algolia search APIs

Several iautos sites (capcar, starterre) and many SaaS apps expose Algolia search directly. `fetchapi algolia` lets you replay those queries without Chrome.

## Algolia call shape

```
POST https://<appId>-dsn.algolia.net/1/indexes/<index>/query
X-Algolia-Application-Id: <appId>
X-Algolia-API-Key:        <searchApiKey>
Content-Type: application/json

{"params":"query=&hitsPerPage=20&page=0&filters=…&facetFilters=…"}
```

`searchApiKey` is the **public** read-only key that ships in the site's JS bundle. It's safe to use in scrapers; it has no admin scope.

## CLI

```bash
ghostchrome fetchapi algolia \
  --app-id 691K8M71IA \
  --api-key 95874bf3cc96f8de61eced3440501724 \
  --index production_cars \
  --params 'query=audi+a3&hitsPerPage=20&page=0' \
  --body-only \
  | jq '{nbHits, nbPages, sample: .hits[0]}'
```

## Real configs (from `iautos/reader-api-vo/sites.json`)

### capcar

```bash
ghostchrome fetchapi algolia \
  --app-id 691K8M71IA \
  --api-key 95874bf3cc96f8de61eced3440501724 \
  --index production_cars \
  --params 'query=&hitsPerPage=100&page=0'
```

Measured: **96 ms** for 64 hits. Page size up to ~1000.

### starterre

```bash
ghostchrome fetchapi algolia \
  --app-id 6LEVMFF03J \
  --api-key c6a36366d0fad5588501dbf13abcbc0f \
  --index app_prod_vehicles \
  --params 'query=&hitsPerPage=100&page=0'
```

Measured: **107 ms** for 5 of 3 463 hits. Bulk: ~35 calls of 100 → ~4 s for full inventory.

## Field schemas

Field names differ per site — check `_params` / first hit before mapping:

| Site | Make | Price | Mileage | URL field |
|---|---|---|---|---|
| capcar | `brand` | `price` | `mileage` | `objectID` → join to slug template |
| starterre | `marqueLibelle` | `prixClient` | `kmCompteur` | `objectID` |

## Bulk pagination

```bash
INDEX=production_cars
PAGE_SIZE=1000
TOTAL=$(ghostchrome fetchapi algolia \
  --app-id 691K8M71IA \
  --api-key 95874bf3cc96f8de61eced3440501724 \
  --index "$INDEX" \
  --params "query=&hitsPerPage=$PAGE_SIZE&page=0" \
  --body-only | jq .nbHits)

PAGES=$(( (TOTAL + PAGE_SIZE - 1) / PAGE_SIZE ))
echo "Will fetch $PAGES pages of $PAGE_SIZE"

for ((p=0; p<PAGES; p++)); do
  ghostchrome fetchapi algolia \
    --app-id 691K8M71IA \
    --api-key 95874bf3cc96f8de61eced3440501724 \
    --index "$INDEX" \
    --params "query=&hitsPerPage=$PAGE_SIZE&page=$p" \
    --body-only | jq -c '.hits[]'
done
```

For maximum throughput, parallelize with `xargs -P 8`. Algolia tends to throttle around 100 req/s on the public API key — keep `-P` reasonable.

## Filter syntax

Algolia filters are passed inside `params` as URL-encoded form values:

```bash
ghostchrome fetchapi algolia \
  --app-id ... --api-key ... --index ... \
  --params 'query=&hitsPerPage=20&filters=price%3E10000%20AND%20year%3A2023&facetFilters=%5B%5B%22brand%3AAudi%22%5D%5D'
```

Plain text equivalent (decode the URL-encoding):

```
query=
hitsPerPage=20
filters=price>10000 AND year:2023
facetFilters=[["brand:Audi"]]
```

Use a tool like `python3 -c "from urllib.parse import quote; print(quote('price>10000 AND year:2023'))"` to encode complex filters cleanly.

## Custom endpoints

If the site uses a custom DSN (private Algolia plan):

```bash
ghostchrome fetchapi algolia \
  --app-id MYAPP \
  --api-key MYKEY \
  --index myindex \
  --endpoint https://search.example.com/algolia-proxy/1/indexes/myindex/query \
  --params 'query=...'
```

## Discovering the keys

If `sites.json` doesn't have the config:

```bash
# Run the SERP through observe and grep for X-Algolia-* headers
ghostchrome --observe watch https://target.fr/search --duration 5s --filter-net=xhr \
  --observe-out events.jsonl

grep -oE 'algolia[a-z]*[: =][^"]+' events.jsonl | sort -u
```

Or directly in browser DevTools: Network → filter `algolia` → check Request Headers.

## Generic JSON APIs

For non-Algolia JSON APIs, drop the `algolia` subcommand:

```bash
ghostchrome fetchapi --method POST \
  --header 'Content-Type=application/json' \
  --header 'Authorization=Bearer xxx' \
  --data '{"query":"..."}' \
  --body-only \
  https://api.example.com/search
```

Same envelope. Same `--body-only` semantics. Same speed.
