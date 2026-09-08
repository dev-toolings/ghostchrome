# Recipe — sweeping a multi-site registry

Pattern for evaluating fast-path coverage across a portfolio of sites. Used during ghostchrome v2 to validate the SSR extractor against the iautos `reader-api-vo` registry (15 sites).

## Goal

For each site, answer in one shot:

- HTTP 200 or blocked?
- What SSR sources does it expose? (`next`, `nuxt`, `apollo`, `jsonld`, `rsc`, …)
- What schema.org types? (`Vehicle`, `Product`, `RealEstateListing`, `AutoDealer`, `ListItem`, …)
- Average latency
- Verdict: fast-path, API replay, or Chrome required

## Script

```bash
SITES_JSON=path/to/sites.json
OUT=/tmp/sweep
mkdir -p "$OUT"

RESULTS="$OUT/results.tsv"
echo -e "id\tcategory\tstatus\tblocked\tsources\tld_types\thas_next\thtml_kb\telapsed_ms\turl" > "$RESULTS"

jq -r '.sites[] | "\(.id)\t\(.category)\t\(.baseUrl)"' "$SITES_JSON" | while IFS=$'\t' read -r ID CAT URL; do
  OUT_FILE="$OUT/${ID}.json"
  S=$(date +%s%3N)
  ghostchrome fastfetch --timeout-ms 6000 "$URL" > "$OUT_FILE" 2>/dev/null
  RC=$?
  E=$(date +%s%3N); WALL=$((E-S))

  if [[ $RC -ne 0 || ! -s "$OUT_FILE" ]]; then
    printf "%s\t%s\tERR\t-\t-\t-\t-\t-\t%s\t%s\n" "$ID" "$CAT" "$WALL" "$URL" >> "$RESULTS"
    continue
  fi

  STATUS=$(jq -r '.status // 0' "$OUT_FILE")
  BLK=$(jq -r 'if .blocked then "yes" else "no" end' "$OUT_FILE")
  SRC=$(jq -r '(.ssr_sources // []) | join(",")' "$OUT_FILE")
  LD=$(jq -r '(.json_ld_types // []) | unique | join(",")' "$OUT_FILE")
  HAS=$(jq -r 'if .has_next_data then "✓" else "·" end' "$OUT_FILE")
  KB=$(jq -r '.html_size // 0' "$OUT_FILE" | awk '{printf "%.0f", $1/1024}')
  MS=$(jq -r '.elapsed_ms // 0' "$OUT_FILE")
  [[ -z "$SRC" ]] && SRC="—"
  [[ -z "$LD" ]] && LD="—"

  printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n" \
    "$ID" "$CAT" "$STATUS" "$BLK" "$SRC" "$LD" "$HAS" "$KB" "$MS" "$URL" >> "$RESULTS"
done

column -t -s$'\t' "$RESULTS"
rm -rf "$OUT"   # always clean scraped data
```

## Result interpretation

Per row, decide the production strategy:

| Pattern | Strategy |
|---|---|
| `sources` includes `next`, `has_next` is `✓` | `fastfetch --next-data` + parse `pageProps.<entity>` |
| `sources` includes `rsc` | `fastfetch --include-payloads` + walk RSC chunks for the listings island |
| `ld_types` includes a relevant schema type (`Vehicle`, `Product`, `RealEstateListing`, …) | parse JSON-LD blocks; suffices for many use cases |
| `sources` is empty AND no `ld_types` | Chrome required (DOM scraping) — use `--fallback-browser` |
| `blocked: yes` | Chrome with `--stealth` + possibly `--user-profile` |
| Site has Algolia/JSON API (visible in Chrome DevTools or in `sites.json`) | `fetchapi algolia` / `fetchapi` — usually faster than HTML even when HTML works |

## Coverage matrix (iautos registry, automotive + real-estate)

After running this sweep on the 11 non-skipped sites of the registry:

| id | category | sources | json_ld_types | verdict |
|---|---|---|---|---|
| autoscout24 | automotive | next, jsonld×3 | Car, BreadcrumbList, Organization | fast-path |
| capcar | automotive | next, jsonld×6 | AutoDealer, Vehicle, WebSite | fast-path + Algolia |
| autosphere | automotive | rsc, jsonld | BreadcrumbList | RSC fast-path |
| viaautomobile | automotive | nuxt-js, jsonld×5 | AutoDealer, FAQPage, ListItem, Organization, WebSite | JSON API |
| transakauto | automotive | — | — | Chrome required |
| elite-auto | automotive | jsonld | WebSite | partial / Chrome |
| paruvendu | automotive | jsonld×2 | Organization, WebSite | partial / Chrome |
| starterre | automotive | — | — | Algolia replay |
| largus | automotive | jsonld | Organization | partial / Chrome |
| pap | real-estate | jsonld | BreadcrumbList | partial / Chrome |
| seloger | real-estate | jsonld×4 | RealEstateListing, Product, BreadcrumbList, Organization | fast-path |

Probing **real listing/SERP URLs** instead of homepages typically promotes the verdict:

- autoscout24 SERP `/lst/<make>/<model>` — full Next data with `Car` JSON-LD (264 ms)
- autoscout24 DETAIL `/offres/<slug>` — full `Product` schema (227 ms)
- seloger SERP `/list.htm?...` — `RealEstateListing` JSON-LD
- viaautomobile JSON API `/eveho/vehicles?page=N` — full structure (911 ms)

## Why this matters

Without sweeping, you'd over-default to Chrome and ship a slow, expensive scraper. With it, you typically find that **70-80% of a portfolio is HTTP-fetchable**, and you only pay Chrome cost for the genuinely-DOM-only minority.

Run the sweep on every new portfolio you onboard.
