#!/bin/sh
# Container health per entrypoint mode (see entrypoint.sh).
#   cdp: Chrome answers /json/version, through the relay port too unless
#        CDP_RELAY=0.
#   run: the ghostchrome binary works (mcp, CLI one-shots, docker exec hosts).
set -eu
case "$(cat /tmp/ghostchrome-mode 2>/dev/null || echo run)" in
  cdp)
    curl -fsS -m 2 http://127.0.0.1:9222/json/version >/dev/null
    if [ "${CDP_RELAY:-1}" != "0" ]; then
      curl -fsS -m 2 http://127.0.0.1:9223/json/version >/dev/null
    fi
    ;;
  *)
    ghostchrome --version >/dev/null
    ;;
esac
