#!/usr/bin/env bash
# search.sh : drop-in replacement for a paid search API (Tavily & co.).
#
# Usage:
#   ./search.sh "climate tech 2026"
#   ./search.sh "rust async runtime" 5 --fetch-content
#
# Emits JSONL on stdout: {"rank","title","url","snippet","source"[,"content"]}
# one object per line : ready to pipe into an LLM agent.
#
# Requires a ghostchrome binary built with recipes:
#   go build -tags recipes -o ghostchrome ./cmd/ghostchrome
set -euo pipefail

QUERY="${1:?usage: search.sh \"<query>\" [results] [extra flags...]}"
RESULTS="${2:-8}"
shift || true
shift 2>/dev/null || true   # drop consumed positional args; keep extra flags in "$@"

# Resolve the ghostchrome binary: prefer one on PATH, else repo-local build.
BIN="$(command -v ghostchrome || true)"
if [[ -z "$BIN" ]]; then
  BIN="$(cd "$(dirname "$0")/../../.." && pwd)/.local/bin/ghostchrome"
fi
if [[ ! -x "$BIN" ]]; then
  echo "error: ghostchrome binary not found. Build it with: go build -tags recipes -o ghostchrome ./cmd/ghostchrome" >&2
  exit 1
fi

# provider=auto → SearXNG (if deployed) then DuckDuckGo fallback. No API key.
exec "$BIN" websearch query "$QUERY" \
  --provider auto \
  --results "$RESULTS" \
  --timeout 45 \
  "$@"
