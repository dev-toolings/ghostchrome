# Anti-bot strategy

How ghostchrome decides between HTTP and Chrome, and what to do when both routes get blocked.

## Decision tree

```
                          ┌─────────────────────┐
                          │   ghostchrome <op>  │
                          └──────────┬──────────┘
                                     │
                            ┌────────▼────────┐
                            │  fastfetch      │   try first (no Chrome)
                            └────────┬────────┘
                                     │
                          ┌──────────┼──────────┐
                          │          │          │
                       blocked    no SSR     payload OK
                          │          │          │
                          └─────┬────┘          │
                                │               │
                  ┌─────────────▼──────────┐    │
                  │ --fallback-browser?    │    │
                  └─────────────┬──────────┘    │
                                │               │
                       ┌────────┼────────┐      │
                       │ yes            no      │
                       │                │       │
                ┌──────▼──────┐    ┌────▼────┐  │
                │  Chrome     │    │  exit   │  │
                │  --stealth  │    │  with   │  │
                │  + retry    │    │  reason │  │
                └──────┬──────┘    └─────────┘  │
                       │                        │
                       └────────────┬───────────┘
                                    │
                            ┌───────▼────────┐
                            │  parse + emit  │
                            └────────────────┘
```

## What `fastfetch` flags

| Signal | Verdict | Notes |
|---|---|---|
| HTTP `403` | blocked | Bare 403, common for DataDome / WAFs |
| HTTP `429` | blocked | Rate-limited |
| HTTP `503` | blocked | Cloudflare interstitial typical |
| `geo.captcha-delivery.com` + body < 30 KB + (`you have been blocked` \| `interstitial` \| `<title>access denied`) | blocked | DataDome interstitial — short body is the discriminator |
| `cf-browser-verification` / `cf_chl_opt` / (`<title>just a moment` + `challenge-platform`) | blocked | Cloudflare |
| `<title>captcha` | blocked | Generic captcha page |
| DataDome SDK loaded on a normal-size page | NOT blocked | False-positive avoidance — the SDK ships on most successful pages too |

When `Blocked == true`, the body content is treated as untrusted: `NextData` and `SSRPayloads` are still extracted but the consumer should fall back rather than parse them.

## Chrome fallback options

### From the CLI

```bash
ghostchrome fastfetch --fallback-browser --stealth https://target.fr
```

Spawns Chrome (respecting `--user-profile`, `--connect`, `--invisible`, `--proxy`), navigates, returns `page.HTML()` re-extracted via the same SSR pipeline. Output envelope's `mode` becomes `"browser"`.

### From a recipe (Go)

```go
res, err := engine.FastFetch(ctx, url, engine.FastFetchOpts{})
if err == nil && !res.Blocked && len(res.SSRPayloads) > 0 {
    // use res.SSRPayloads / res.NextData
    return parseDirectly(res)
}

// fall back
opts := buildBrowserOpts()
b, page := openSession(opts)
defer b.Close()
engine.Navigate(page, url, "load")
// extract via Chrome path
```

This is the pattern wired into `packages/autoscout24/autoscout24.go` (search and detail) and easy to copy into other recipes.

## Stealth ladder

Order of escalation for hard targets:

1. **`fastfetch` only** — works on most SSR sites including autoscout24 SERP, capcar, leboncoin, autosphere (via RSC).
2. **`fastfetch --fallback-browser --stealth`** — Chrome with anti-detection patches. Resolves most DataDome / Cloudflare interstitials.
3. **`--invisible` mode** — headful off-screen. Real GPU/fonts. Defeats fingerprinters that detect headless rendering.
4. **`--user-profile <name>`** with `ghostchrome login <site>` first — stamps the device cookie that DataDome / LinkedIn-style binding requires.
5. **`--proxy http://user:pass@host:port`** — residential / mobile IP when datacenter IPs are blocklisted.
6. **`--default-extensions`** — uBlock Lite + ICDC + Force-BG to look like a real user's Chrome.

## What works in practice (iautos registry)

Across the 11-site sweep documented in [`recipes/registry-sweep.md`](./recipes/registry-sweep.md):

| Status | Sites | How |
|---|---|---|
| Direct fast-path | autoscout24, capcar, viaautomobile (home), seloger (SERP) | `fastfetch` with SSR sources |
| API replay | capcar, starterre (Algolia), viaautomobile (Eveho) | `fetchapi` |
| RSC chunks | autosphere | `ExtractRSCPayloads` |
| Chrome required | transakauto, starterre HTML | `--fallback-browser` |

## Recovery in the agent loop

The Chrome path's agent (`ghostchrome agent`) reacts to anti-bot at runtime:

```go
// engine/recovery.go: built-in chain
DefaultRecoveryHooks = []RecoveryHook{
    RecoverStaleRef,         // surface explicit error, no silent re-extract
    RecoverDialogAccept,     // dialog open → accept → retry
    RecoverBotChallenge,     // DataDome/Cloudflare → wait 10s → retry
    RecoverNetworkSettle,    // timeout → wait stable 2s → retry
}
```

`RecoverBotChallenge` reuses `engine/preview.go:WaitForBotChallenge`, which short-circuits cheaply when no challenge is detected, so the cost is only paid when needed.

## Detecting silently broken pages

A response can be HTTP 200 but functionally empty (DataDome serves a cookie-test page that returns 200 with a minimal HTML). `fastfetch` is conservative — it does NOT flag these — but recipes can add a check:

```go
res, _ := engine.FastFetch(ctx, url, opts)
if !res.Blocked && len(res.SSRPayloads) == 0 && len(res.HTML) < 30_000 {
    // Suspicious: 200 with no content, no SSR data, short body.
    // Fall back to Chrome.
}
```

Or simpler: assert that the parsed payload has the structure you expect (e.g. `pageProps.listings` is a non-empty array). If not, fall back.
