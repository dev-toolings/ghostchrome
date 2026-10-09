#!/bin/sh
# `cdp` starts a bare headless Chrome and publishes its DevTools endpoint on
# 0.0.0.0:9223 for a ghostchrome running outside the container. Chrome binds
# remote debugging to loopback only, so socat relays the port. Any other
# argument list runs ghostchrome inside the container.
set -eu

if [ "${1:-}" != "cdp" ]; then
  exec ghostchrome "$@"
fi
shift

profile="${CHROME_PROFILE_DIR:-$HOME/chrome-profile}"
mkdir -p "$profile"
socat TCP-LISTEN:9223,fork,reuseaddr,bind=0.0.0.0 TCP:127.0.0.1:9222 &
# exec keeps Chrome as the container's main process: the container stops when
# Chrome does, and tini reaps both.
exec google-chrome-stable \
  --headless=new \
  --no-sandbox \
  --remote-debugging-port=9222 \
  --user-data-dir="$profile" \
  --no-first-run \
  --no-default-browser-check \
  "$@" \
  about:blank
