# Ubuntu + Chrome image

Ubuntu 24.04, Google Chrome stable (amd64 only) and the ghostchrome binary
built from this checkout. Usage, the CDP mode and its trade-offs are
documented in [`docs/cdp.md`](../../../docs/cdp.md).

```bash
# From the repository root
docker build -f tools/deploy/chrome/Dockerfile -t ghostchrome .

docker run -i --rm --shm-size=1g ghostchrome mcp                         # MCP stdio
docker run --rm --shm-size=1g ghostchrome preview https://example.com    # CLI
docker run -d --shm-size=1g -p 127.0.0.1:9222:9223 ghostchrome cdp       # CDP
```

- `entrypoint.sh` runs `ghostchrome "$@"`, or a bare headless Chrome with a
  socat relay on port 9223 for `cdp`. Extra `cdp` arguments go to Chrome.
- The container runs as the unprivileged `ghost` user with
  `GHOSTCHROME_NO_SANDBOX=1`: the container is the isolation boundary.
- `CHROME_PROFILE_DIR` moves the `cdp` profile, for example onto a volume.
- `healthcheck.sh` follows the mode the entrypoint recorded: `cdp` probes
  Chrome on 9222 and the relay on 9223, every other mode runs
  `ghostchrome --version`. CI fails unless both modes report `healthy`. If you
  replace the entrypoint, replace the healthcheck too.
- Release tags push `ghcr.io/dev-toolings/ghostchrome:<tag>` and `:latest`;
  CI builds the image and smoke-tests all three modes on every push.
