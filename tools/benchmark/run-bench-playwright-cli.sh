#!/usr/bin/env bash
# run-bench-playwright-cli.sh: head-to-head benchmark: ghostchrome vs Playwright CLI.
#
# Usage:
#   ./tools/benchmark/run-bench-playwright-cli.sh                  # cold-spawn mode
#   BENCH_MODE=warm ./tools/benchmark/run-bench-playwright-cli.sh  # session reuse mode
#
# Env:
#   TRIALS                 number of trials per site
#   BENCH_MODE             cold (default) | warm
#   BENCH_SITES            comma-separated label list, e.g. "landing,product"
#   SKIP_PUBLIC            if set, skip public URLs
#   PWCLI_SESSION          Playwright-CLI session name in warm mode
#   PWCLI_PACKAGE          Playwright CLI package spec passed to bunx
#   FIXTURE_PORT           fixture HTTP port
#   PWMCLI_EXTRA_ARGS       extra args appended to every playwright-cli command
#
# Requires: go, bun, node, /usr/bin/time, python3

set -euo pipefail

cd "$(dirname "$0")/../.."
ROOT=$(pwd)

TRIALS=${TRIALS:-3}
FIXTURE_PORT=${FIXTURE_PORT:-4848}
BENCH_MODE=${BENCH_MODE:-cold}
SERVE_PORT=${SERVE_PORT:-9223}
PWCLI_SESSION=${PWCLI_SESSION:-playwright-cli-bench}
PWCLI_PACKAGE=${PWCLI_PACKAGE:-@playwright/cli@latest}

TIMECMD=/usr/bin/time
if ! [ -x "$TIMECMD" ]; then
  echo "fatal: /usr/bin/time not found" >&2
  exit 1
fi

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "fatal: '$1' not found in PATH" >&2; exit 1; }
}
need go
need bun
need node
need curl
need python3

export PATH="$HOME/.local/go/bin:$PATH"

echo "[bench] building ghostchrome"
mkdir -p "$ROOT/.local/bin"
go build -o "$ROOT/.local/bin/ghostchrome" ./cmd/ghostchrome
GHOSTBIN="$ROOT/.local/bin/ghostchrome"

echo "[bench] building microbench"
go build -o "$ROOT/.local/bin/microbench" ./tools/benchmark/cmd/microbench/

echo "[bench] playwright-cli package: $PWCLI_PACKAGE"

echo "[bench] starting fixture server on :$FIXTURE_PORT"
python3 -m http.server "$FIXTURE_PORT" --directory "$ROOT/tools/benchmark/fixtures" >"$ROOT/tools/benchmark/.fixtures.log" 2>&1 &
FIXTURE_PID=$!
SERVE_PID=""

cleanup() {
  if [ -n "$FIXTURE_PID" ] && kill -0 "$FIXTURE_PID" 2>/dev/null; then
    kill "$FIXTURE_PID" 2>/dev/null || true
  fi
  if [ -n "$SERVE_PID" ] && kill -0 "$SERVE_PID" 2>/dev/null; then
    kill "$SERVE_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

for _ in $(seq 1 20); do
  if curl -sf "http://localhost:$FIXTURE_PORT/landing.html" >/dev/null; then
    break
  fi
  sleep 0.2
done

ALL_SITES=(
  "landing|http://localhost:$FIXTURE_PORT/landing.html"
  "product|http://localhost:$FIXTURE_PORT/product.html"
  "search|http://localhost:$FIXTURE_PORT/search.html"
  "dashboard|http://localhost:$FIXTURE_PORT/dashboard.html"
  "news|http://localhost:$FIXTURE_PORT/news.html"
)

if [ -z "${SKIP_PUBLIC:-}" ]; then
  ALL_SITES+=(
    "example|https://example.com"
    "hn|https://news.ycombinator.com"
    "github|https://github.com/dev-toolings/ghostchrome"
  )
fi

if [ -n "${BENCH_SITES:-}" ]; then
  WANT=",${BENCH_SITES},"
  FILTERED=()
  for entry in "${ALL_SITES[@]}"; do
    LABEL="${entry%%|*}"
    if [[ $WANT == *",$LABEL,"* ]]; then
      FILTERED+=("$entry")
    fi
  done
  ALL_SITES=("${FILTERED[@]}")
fi

TRIALS_DIR="$ROOT/tools/benchmark/trials"
rm -rf "$TRIALS_DIR"
mkdir -p "$TRIALS_DIR"

GC_WS=""

run_ghostchrome() {
  local site="$1" url="$2" trial="$3"
  local stdout_file="$TRIALS_DIR/.gc.${site}.${trial}.out"
  local time_file="$TRIALS_DIR/.gc.${site}.${trial}.time"
  local extra=""
  if [ "$BENCH_MODE" = "warm" ] && [ -n "$GC_WS" ]; then
    extra="--connect $GC_WS"
  fi

  $TIMECMD -f "%e %M" -o "$time_file" \
    "$GHOSTBIN" preview "$url" --wait load $extra >"$stdout_file" 2>/dev/null || true

  local bytes seconds rss_kb ms
  bytes=$(wc -c < "$stdout_file")
  seconds=$(awk 'NR==1{print $1}' "$time_file" 2>/dev/null)
  rss_kb=$(awk 'NR==1{print $2}' "$time_file" 2>/dev/null)
  [[ "$seconds" =~ ^[0-9.]+$ ]] || seconds=0
  [[ "$rss_kb" =~ ^[0-9]+$ ]] || rss_kb=0
  ms=$(awk -v s="$seconds" 'BEGIN{printf "%d", s*1000}')
  cat >"$TRIALS_DIR/${site}_${trial}_ghostchrome.json" <<EOF
{"tool":"ghostchrome","site":"$site","url":"$url","bytes":$bytes,"durationMs":$ms,"peakRssKb":$rss_kb}
EOF
  rm -f "$stdout_file" "$time_file"
}

run_playwright_cli() {
  local site="$1" url="$2" trial="$3"
  local stdout_file="$TRIALS_DIR/.pwcli.${site}.${trial}.out"
  local time_file="$TRIALS_DIR/.pwcli.${site}.${trial}.time"
  local session="$PWCLI_SESSION"

  local extra=(
    "-y"
    "$PWCLI_PACKAGE"
  )
  if [ "${PWMCLI_EXTRA_ARGS:-}" != "" ]; then
    # shellcheck disable=SC2206
    extra+=(
      ${PWMCLI_EXTRA_ARGS}
    )
  fi

  if [ "$BENCH_MODE" != "warm" ]; then
    session="playwright-cli-${site}-${trial}"
  fi

  # Playwright CLI emits a snapshot as part of `open`/`goto`.
  # We measure the single navigation command only; adding explicit `snapshot`
  # here would count the same surface twice and distort latency/bytes.
  local bytes=0 seconds=0 rss_kb=0 ms
  if [ "$BENCH_MODE" = "warm" ]; then
    "$TIMECMD" -f "%e %M" -o "$time_file" \
      bunx "${extra[@]}" -s "$session" goto "$url" >"$stdout_file" 2>/dev/null || true
  else
    "$TIMECMD" -f "%e %M" -o "$time_file" \
      bunx "${extra[@]}" -s "$session" open "$url" --browser=chrome >"$stdout_file" 2>/dev/null || true
  fi

  bytes=$(wc -c < "$stdout_file")
  seconds=$(awk 'NR==1{print $1}' "$time_file" 2>/dev/null)
  rss_kb=$(awk 'NR==1{print $2}' "$time_file" 2>/dev/null)
  [[ "$seconds" =~ ^[0-9.]+$ ]] || seconds=0
  [[ "$rss_kb" =~ ^[0-9]+$ ]] || rss_kb=0
  ms=$(awk -v s="$seconds" 'BEGIN{printf "%d", s*1000}')
  cat >"$TRIALS_DIR/${site}_${trial}_playwright-cli.json" <<EOF
{"tool":"playwright-cli","site":"$site","url":"$url","bytes":$bytes,"durationMs":$ms,"peakRssKb":$rss_kb}
EOF
  rm -f "$stdout_file" "$time_file"
}

if [ "$BENCH_MODE" = "warm" ]; then
  echo "[bench] warm mode: starting ghostchrome serve on :$SERVE_PORT"
  "$GHOSTBIN" serve --port "$SERVE_PORT" >"$ROOT/tools/benchmark/.serve.log" 2>&1 &
  SERVE_PID=$!
  for _ in $(seq 1 30); do
    GC_WS=$(grep -oE "ws://127\.0\.0\.1:$SERVE_PORT/devtools/browser/[a-f0-9-]+" "$ROOT/tools/benchmark/.serve.log" 2>/dev/null | head -1 || true)
    if [ -n "$GC_WS" ]; then
      break
    fi
    sleep 0.2
  done
  if [ -z "$GC_WS" ]; then
    echo "[bench] ghostchrome warm mode failed to capture WS URL; falling back to cold"
    BENCH_MODE=cold
  fi

  echo "[bench] warm mode: bootstrapping playwright-cli session $PWCLI_SESSION"
  if [ "$BENCH_MODE" = "warm" ]; then
    bunx -y "$PWCLI_PACKAGE" -s "$PWCLI_SESSION" open https://example.com --browser=chrome >/dev/null 2>&1 || {
    echo "[bench] playwright-cli session bootstrap failed, falling back to cold"
    BENCH_MODE=cold
    }
  fi
fi

if [ "$BENCH_MODE" = "warm" ]; then
  for entry in "${ALL_SITES[@]}"; do
    SITE="${entry%%|*}"
    URL="${entry#*|}"
    echo "[bench] $SITE"
    for t in $(seq 1 "$TRIALS"); do
      printf "  trial %d/%d  ghostchrome…" "$t" "$TRIALS"
      run_ghostchrome "$SITE" "$URL" "$t"
      printf " playwright-cli…"
      run_playwright_cli "$SITE" "$URL" "$t"
      echo " ok"
    done
  done
else
  for entry in "${ALL_SITES[@]}"; do
    SITE="${entry%%|*}"
    URL="${entry#*|}"
    echo "[bench] $SITE  ($URL)"
    for t in $(seq 1 "$TRIALS"); do
      printf "  trial %d/%d  ghostchrome…" "$t" "$TRIALS"
      run_ghostchrome "$SITE" "$URL" "$t"
      printf " playwright-cli…"
      run_playwright_cli "$SITE" "$URL" "$t"
      echo " ok"
    done
  done
fi

bunx -y "$PWCLI_PACKAGE" close-all >/dev/null 2>&1 || true

if [ "$BENCH_MODE" = "warm" ]; then
  MD_OUT="$ROOT/tools/benchmark/results-cli-warm.md"
  JSON_OUT="$ROOT/tools/benchmark/results-cli-warm.json"
  BADGES_DIR="$ROOT/tools/benchmark/badges-cli-warm"
else
  MD_OUT="$ROOT/tools/benchmark/results-cli.md"
  JSON_OUT="$ROOT/tools/benchmark/results-cli.json"
  BADGES_DIR="$ROOT/tools/benchmark/badges-cli"
fi

"$ROOT/.local/bin/microbench" \
  --input "$TRIALS_DIR" \
  --md "$MD_OUT" \
  --json "$JSON_OUT" \
  --badges "$BADGES_DIR" \
  --binary "$GHOSTBIN" \
  --mode "$BENCH_MODE" \
  --opponent playwright-cli \
  --opponent-label "Playwright CLI"
