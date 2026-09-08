#!/usr/bin/env bash
# run-bench.sh: head-to-head benchmark: ghostchrome vs Playwright-MCP.
#
# For each site × trial, captures payload bytes, wall ms, peak RSS for both
# tools, then aggregates into tools/benchmark/results.{md,json} + badge JSONs.
#
# Usage:    ./tools/benchmark/run-bench.sh
# Env:      TRIALS=3                       trials per (site, tool)
#           PWMCP_CHROME_PATH=/path/chrome  pin Chrome for Playwright-MCP
#           BENCH_SITES="<labels>"          comma-separated subset, e.g. "landing,product"
#           SKIP_PUBLIC=1                   skip the 3 real-world URLs (CI / offline)
#
# Requires: go, node, npx, python3, /usr/bin/time

set -euo pipefail

cd "$(dirname "$0")/../.."
ROOT=$(pwd)

TRIALS=${TRIALS:-3}
FIXTURE_PORT=${FIXTURE_PORT:-4848}
BENCH_MODE=${BENCH_MODE:-cold}   # cold | warm
SERVE_PORT=${SERVE_PORT:-9223}   # avoid colliding with a user-run serve on 9222
TIMECMD=/usr/bin/time
if ! [ -x "$TIMECMD" ]; then
  echo "fatal: /usr/bin/time not found (apt install time on Debian/Ubuntu)" >&2
  exit 1
fi

# ---------- toolchain checks ----------
need() { command -v "$1" >/dev/null 2>&1 || { echo "fatal: '$1' not found in PATH" >&2; exit 1; }; }
# Make go discoverable if the user installed it under ~/.local/go.
export PATH="$HOME/.local/go/bin:$PATH"
need go
need node
need npx
need python3

# ---------- build ghostchrome ----------
echo "[bench] building ghostchrome…"
mkdir -p "$ROOT/.local/bin"
go build -o "$ROOT/.local/bin/ghostchrome" ./cmd/ghostchrome
GHOSTBIN="$ROOT/.local/bin/ghostchrome"

# ---------- pre-build microbench ----------
echo "[bench] building microbench aggregator…"
go build -o "$ROOT/.local/bin/microbench" ./tools/benchmark/cmd/microbench/

# ---------- pin Chromium for Playwright-MCP (reuse Rod's download) ----------
if [ -z "${PWMCP_CHROME_PATH:-}" ]; then
  CAND=$(ls -d "$HOME/.cache/rod/browser/chromium-"*/chrome 2>/dev/null | head -1 || true)
  if [ -n "$CAND" ] && [ -x "$CAND" ]; then
    export PWMCP_CHROME_PATH="$CAND"
    echo "[bench] PWMCP_CHROME_PATH=$PWMCP_CHROME_PATH (reusing Rod's Chromium)"
  fi
fi

# ---------- boot fixture HTTP server ----------
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
# Wait until the server answers.
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if curl -sf "http://localhost:$FIXTURE_PORT/landing.html" >/dev/null; then
    break
  fi
  sleep 0.2
done

# ---------- site matrix ----------
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

# ---------- per-trial harness ----------
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
  local bytes; bytes=$(wc -c < "$stdout_file")
  # /usr/bin/time writes "%e %M" on its first line, but appends a
  # "Command exited with non-zero status N" line when the wrapped command
  # fails. Pull only the first whitespace-separated numeric pair to keep
  # the JSON output well-formed even if ghostchrome itself bailed.
  local seconds rss_kb
  seconds=$(awk 'NR==1{print $1}' "$time_file" 2>/dev/null)
  rss_kb=$(awk 'NR==1{print $2}' "$time_file" 2>/dev/null)
  [[ "$seconds" =~ ^[0-9.]+$ ]] || seconds=0
  [[ "$rss_kb" =~ ^[0-9]+$ ]] || rss_kb=0
  local ms; ms=$(awk -v s="$seconds" 'BEGIN{printf "%d", s*1000}')
  cat >"$TRIALS_DIR/${site}_${trial}_ghostchrome.json" <<EOF
{"tool":"ghostchrome","site":"$site","url":"$url","bytes":$bytes,"durationMs":$ms,"peakRssKb":$rss_kb}
EOF
  rm -f "$stdout_file" "$time_file"
}

run_pwmcp() {
  local site="$1" url="$2" trial="$3"
  local out_file="$TRIALS_DIR/.pw.${site}.${trial}.out"
  local time_file="$TRIALS_DIR/.pw.${site}.${trial}.time"
  # Cold-mode: wall-time the whole `node driver $url` invocation so we count
  # Node startup + MCP init + navigate + snapshot: apples-to-apples with
  # ghostchrome's full cold-spawn measurement.
  $TIMECMD -f "%e %M" -o "$time_file" \
    node "$ROOT/tools/benchmark/pwmcp-driver.mjs" "$url" >"$out_file" 2>/dev/null || true
  if [ ! -s "$out_file" ]; then
    cat >"$TRIALS_DIR/${site}_${trial}_playwright-mcp.json" <<EOF
{"tool":"playwright-mcp","site":"$site","url":"$url","bytes":0,"durationMs":0,"peakRssKb":0}
EOF
    rm -f "$out_file" "$time_file"
    return
  fi
  local bytes peak_rss seconds wrapper_rss
  bytes=$(node -e 'let s="";process.stdin.on("data",b=>s+=b).on("end",()=>{const j=JSON.parse(s);console.log(j.bytes)})' < "$out_file")
  peak_rss=$(node -e 'let s="";process.stdin.on("data",b=>s+=b).on("end",()=>{const j=JSON.parse(s);console.log(j.peakRssKb||0)})' < "$out_file")
  local seconds wrapper_rss
  seconds=$(awk 'NR==1{print $1}' "$time_file" 2>/dev/null)
  wrapper_rss=$(awk 'NR==1{print $2}' "$time_file" 2>/dev/null)
  [[ "$seconds" =~ ^[0-9.]+$ ]] || seconds=0
  [[ "$wrapper_rss" =~ ^[0-9]+$ ]] || wrapper_rss=0
  local ms; ms=$(awk -v s="$seconds" 'BEGIN{printf "%d", s*1000}')
  if [ "$wrapper_rss" -gt "$peak_rss" ]; then peak_rss=$wrapper_rss; fi
  cat >"$TRIALS_DIR/${site}_${trial}_playwright-mcp.json" <<EOF
{"tool":"playwright-mcp","site":"$site","url":"$url","bytes":$bytes,"durationMs":$ms,"peakRssKb":$peak_rss}
EOF
  rm -f "$out_file" "$time_file"
}

# ---------- warm-mode setup ----------
if [ "$BENCH_MODE" = "warm" ]; then
  echo "[bench] warm mode: starting ghostchrome serve on :$SERVE_PORT"
  "$GHOSTBIN" serve --port "$SERVE_PORT" >"$ROOT/tools/benchmark/.serve.log" 2>&1 &
  SERVE_PID=$!
  # Wait for the WS URL to appear in the log.
  for _ in $(seq 1 30); do
    GC_WS=$(grep -oE "ws://127\.0\.0\.1:$SERVE_PORT/devtools/browser/[a-f0-9-]+" "$ROOT/tools/benchmark/.serve.log" 2>/dev/null | head -1 || true)
    if [ -n "$GC_WS" ]; then break; fi
    sleep 0.2
  done
  if [ -z "$GC_WS" ]; then
    echo "[bench] warm mode failed to discover serve WS URL; falling back to cold"
    BENCH_MODE=cold
  else
    echo "[bench] GC_WS=$GC_WS"
  fi
fi

# ---------- main loop ----------
if [ "$BENCH_MODE" = "warm" ]; then
  # Warm pw-mcp: one driver invocation per trial, all URLs piped on stdin so
  # the MCP server is reused across snapshots (per-call durationMs reflects
  # only navigate+snapshot, not boot).
  for t in $(seq 1 "$TRIALS"); do
    echo "[bench] trial $t/$TRIALS  ghostchrome (warm)…"
    for entry in "${ALL_SITES[@]}"; do
      SITE="${entry%%|*}"
      URL="${entry#*|}"
      run_ghostchrome "$SITE" "$URL" "$t"
    done
    echo "[bench] trial $t/$TRIALS  pw-mcp (warm)…"
    URL_FILE="$TRIALS_DIR/.urls.${t}"
    : >"$URL_FILE"
    declare -A SITE_OF_URL=()
    for entry in "${ALL_SITES[@]}"; do
      SITE="${entry%%|*}"
      URL="${entry#*|}"
      printf '%s\n' "$URL" >>"$URL_FILE"
      SITE_OF_URL["$URL"]="$SITE"
    done
    DRIVER_OUT="$TRIALS_DIR/.pw.warm.${t}.jsonl"
    node "$ROOT/tools/benchmark/pwmcp-driver.mjs" < "$URL_FILE" >"$DRIVER_OUT" 2>/dev/null || true
    # Split per-URL records into per-site JSON files for the aggregator.
    while IFS= read -r line; do
      [ -z "$line" ] && continue
      U=$(node -e 'let s="";process.stdin.on("data",b=>s+=b).on("end",()=>{console.log(JSON.parse(s).url)})' <<<"$line")
      SITE="${SITE_OF_URL[$U]:-unknown}"
      echo "$line" | node -e 'let s="";process.stdin.on("data",b=>s+=b).on("end",()=>{const o=JSON.parse(s);o.site=process.argv[1];process.stdout.write(JSON.stringify(o)+"\n")})' "$SITE" \
        > "$TRIALS_DIR/${SITE}_${t}_playwright-mcp.json"
    done < "$DRIVER_OUT"
    rm -f "$URL_FILE" "$DRIVER_OUT"
  done
else
  for entry in "${ALL_SITES[@]}"; do
    SITE="${entry%%|*}"
    URL="${entry#*|}"
    echo "[bench] $SITE  ($URL)"
    for t in $(seq 1 "$TRIALS"); do
      printf "  trial %d/%d  ghostchrome…" "$t" "$TRIALS"
      run_ghostchrome "$SITE" "$URL" "$t"
      printf " pw-mcp…"
      run_pwmcp "$SITE" "$URL" "$t"
      echo " ok"
    done
  done
fi

# ---------- aggregate ----------
echo "[bench] aggregating…"
if [ "$BENCH_MODE" = "warm" ]; then
  MD_OUT="$ROOT/tools/benchmark/results-warm.md"
  JSON_OUT="$ROOT/tools/benchmark/results-warm.json"
  BADGES_DIR="$ROOT/tools/benchmark/badges-warm"
else
  MD_OUT="$ROOT/tools/benchmark/results.md"
  JSON_OUT="$ROOT/tools/benchmark/results.json"
  BADGES_DIR="$ROOT/tools/benchmark/badges"
fi
"$ROOT/.local/bin/microbench" \
  --input "$TRIALS_DIR" \
  --md "$MD_OUT" \
  --json "$JSON_OUT" \
  --badges "$BADGES_DIR" \
  --binary "$GHOSTBIN" \
  --mode "$BENCH_MODE"

echo
echo "[bench] done ($BENCH_MODE). See $MD_OUT"
