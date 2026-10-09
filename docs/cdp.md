# Driving an existing Chrome over CDP

By default every command spawns (or reuses) a background Chrome that
ghostchrome owns. This page covers the two other setups: attaching to a Chrome
you run yourself, and running Chrome in a Docker container.

```text
+---------------------------+   +---------------------------+
| A. Your own Chrome        |   | B. Docker: Ubuntu+Chrome  |
| dedicated profile, 9222   |   | image tools/deploy/chrome |
+-------------+-------------+   +-------------+-------------+
              |                               |
              v                               v
+-------------+-------------+   +-------------+-------------+
| ghostchrome on the host   |   | ghostchrome inside, or    |
| attach --cdp=http://...   |   | host attach to 9222 (cdp) |
+---------------------------+   +---------------------------+
```

| | A. Your own Chrome | B. Docker |
|---|---|---|
| Fingerprint | Real desktop Chrome, your extensions | Headless Linux Chrome, easier to flag |
| Sessions | Logins stay in the dedicated profile | Ephemeral unless you mount a volume |
| Visible | Yes, you can watch and take over | No |
| Isolation | None: the page runs on your desktop | Container boundary |
| File paths (`upload`, downloads) | Shared with the host | Seen by the container's Chrome |
| Fit | Daily agent work on your machine | Servers, CI, disposable runs |

## A. Attach to your own Chrome

Chrome refuses remote debugging on its default profile directory, so give it a
dedicated one. Logins you make there persist across restarts.

```bash
# Linux (macOS: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
google-chrome --remote-debugging-port=9222 \
  --user-data-dir="$HOME/.ghostchrome/chrome-attached" &

# CLI: register it as the default session, then use any command.
ghostchrome attach --cdp=http://127.0.0.1:9222
ghostchrome goto https://example.com

# MCP: point the server at it.
claude mcp add ghostchrome -- \
  bunx github:dev-toolings/ghostchrome mcp --connect http://127.0.0.1:9222
```

The standalone `ghostchrome-mcp` binary reads the same endpoint from
`GHOSTCHROME_CONNECT`. `auto` instead of a URL finds a Chrome on ports
9222-9229 without naming one.

The DevTools port has no authentication: any local process can drive that
Chrome and every site it is signed in to. Keep it on loopback, keep the
profile dedicated to agents, and close Chrome when you are done.

## B. Docker: Ubuntu + Chrome

`tools/deploy/chrome/` builds an amd64 image with Google Chrome and the
ghostchrome binary of the checkout. Release tags publish it to
`ghcr.io/dev-toolings/ghostchrome`.

```bash
docker build -f tools/deploy/chrome/Dockerfile -t ghostchrome .
```

**ghostchrome inside the container** (recommended). Nothing listens on a port,
and file paths stay consistent because Chrome and ghostchrome share a
filesystem.

```bash
# MCP server over stdio
claude mcp add ghostchrome -- docker run -i --rm --shm-size=1g ghostchrome mcp

# One CLI command
docker run --rm --shm-size=1g ghostchrome preview https://example.com
```

**CDP for a ghostchrome on the host.** The `cdp` mode starts a bare headless
Chrome and relays its DevTools port to 9223 in the container (Chrome itself
only binds loopback). Publish it on the host loopback only.

```bash
docker run -d --name gc-chrome --shm-size=1g -p 127.0.0.1:9222:9223 ghostchrome cdp
ghostchrome attach --cdp=http://127.0.0.1:9222
ghostchrome mcp --connect http://127.0.0.1:9222             # MCP equivalent
```

In this mode `upload` paths and downloads refer to the container's filesystem,
not the host's. Mount a shared volume at the same path on both sides if you
need them.

Remote CDP endpoints (Browserless and similar) follow the same pattern: pass
their `ws(s)://` or `http(s)://` endpoint to `attach --cdp`, `--connect` or
`GHOSTCHROME_CONNECT`.
