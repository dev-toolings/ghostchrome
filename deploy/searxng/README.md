# SearXNG — primary provider for `ghostchrome websearch`

Self-hosted metasearch that exposes a stable **JSON** endpoint. It is the
primary search provider (`text → [urls]`); ghostchrome only proxies an index,
it never rebuilds one. DuckDuckGo HTML is the zero-dependency fallback when
this container is down.

## Prerequisites

The shared dev infra must be up (`infra-redis` on `dev-shared-net`):

```bash
docker ps | grep infra-redis        # must be running
```

If `dev-shared-net` does not exist yet, start the `dev-infra` stack first
(it owns the external network — this compose never recreates it).

## Run

```bash
# optional: set a real instance secret (never commit it)
export SEARXNG_SECRET="$(openssl rand -hex 32)"

# host port 8080 taken? override it (and point the recipe at it, see below)
export SEARXNG_PORT=8089

docker compose -f deploy/searxng/docker-compose.yml up -d
```

Point the recipe at a non-default port without passing `--searxng-url` every time:

```bash
export GHOSTCHROME_SEARXNG_URL=http://localhost:8089
```

## Verify (PRD Phase 0 acceptance)

```bash
# JSON format enabled + results returned (use the JSON API, not the HTML root)
curl -s 'http://localhost:8089/search?q=anthropic&format=json' | jq '.results | length'
# → a number > 0 (NOT "403 format not enabled")
```

## Clean logs

`settings.yml` + `limiter.toml` are tuned for **zero warnings/errors** on a
private instance: Tor engines (ahmia/torch) removed, flaky engines
(brave/duckduckgo) disabled, `valkey.url` (not the deprecated `redis.url`),
and the current `botdetection.*` limiter schema. The recipe queries only the
JSON API (`/search?format=json`) with an `X-Forwarded-For` header, which never
trips botdetection. Hitting the **HTML root `/`** without that header (a naive
health check or a browser visit) is the one thing that logs a botdetection
error — probe the JSON endpoint instead.

Then the recipe:

```bash
go build -tags recipes -o ghostchrome ./cmd/ghostchrome
./ghostchrome websearch query "golang concurrency" --provider searxng
```

## Notes

- **Cache**: `redis://infra-redis:6379/3` — logical DB 3, no local Redis added.
- **Not for public exposure**: `server.limiter` is disabled so our HTTP client
  can reach the JSON API. Keep this instance private (dev/perso). See PRD §7.
- **Prod**: pin `image:` to a dated tag/digest instead of `latest`.
