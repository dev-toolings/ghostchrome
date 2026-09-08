#!/usr/bin/env bash
# Re-measure the REAL result shape of every JSONL agent op against the live binary.
#
# This is the "stay truthful" tool. The agent loop is the ground truth the SDKs
# and contract must match — never assume, MEASURE. Run this whenever the agent
# surface or the SDKs change, then make sdk/typescript + sdk/python match what it
# prints. The shapes are read from the running binary, so they cannot drift.
#
# Requires a Chrome on :9222 (the agent attaches via --connect=auto):
#   google-chrome --headless=new --remote-debugging-port=9222 \
#     --user-data-dir=/tmp/gc-measure about:blank &
#
# Usage: scripts/measure-agent-ops.sh [url]
set -euo pipefail
cd "$(dirname "$0")/.."

BIN="${GHOSTCHROME_BIN:-./ghostchrome}"
URL="${1:-https://example.com}"

if [ ! -x "$BIN" ]; then
  go build -o ghostchrome ./cmd/ghostchrome
  BIN=./ghostchrome
fi

if ! curl -s --max-time 2 http://127.0.0.1:9222/json/version >/dev/null 2>&1; then
  echo "No Chrome on :9222. Start one first:" >&2
  echo "  google-chrome --headless=new --remote-debugging-port=9222 --user-data-dir=/tmp/gc-measure about:blank &" >&2
  exit 1
fi

printf '%s\n' \
  '{"id":"init","op":"init"}' \
  "{\"id\":\"navigate\",\"op\":\"navigate\",\"args\":{\"url\":\"${URL}\"}}" \
  '{"id":"extract","op":"extract","args":{"level":"skeleton"}}' \
  '{"id":"eval","op":"eval","args":{"expr":"document.title"}}' \
  '{"id":"url","op":"url"}' \
  '{"id":"scroll_by","op":"scroll_by","args":{"dy":100}}' \
  '{"id":"scroll_to","op":"scroll_to","args":{"bottom":true}}' \
  '{"id":"wait","op":"wait","args":{"ms":50}}' \
  '{"id":"errors","op":"errors"}' \
  '{"id":"screenshot","op":"screenshot","args":{"quality":40}}' \
  '{"id":"close","op":"close"}' \
  | "$BIN" agent --connect=auto 2>/dev/null \
  | python3 "$(dirname "$0")/measure_shapes.py"
