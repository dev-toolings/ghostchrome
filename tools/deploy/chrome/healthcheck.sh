#!/bin/sh
# Container health per entrypoint mode (see entrypoint.sh).
#   cdp: Chrome answers /json/version, through the published relay port too.
#   run: the ghostchrome binary works (mcp, CLI one-shots, docker exec hosts).
set -eu
case "$(cat /tmp/ghostchrome-mode 2>/dev/null || echo run)" in
  cdp)
    curl -fsS -m 2 http://127.0.0.1:9222/json/version >/dev/null
    curl -fsS -m 2 http://127.0.0.1:9223/json/version >/dev/null
    ;;
  *)
    ghostchrome --version >/dev/null
    ;;
esac
