#!/usr/bin/env bash
# run-e2e.sh : Voie A validée 100× : Claude Code (claude -p, headless) pilote
# le navigateur via le MCP ghostchrome isolé (--strict-mcp-config).
#
# Chaque cas = un vrai run `claude -p` → vrai Chrome → vrai MCP → assertion sur
# la sortie. Cibles RFC 2606 (example.com/.org/.net) : réelles, jamais rate-
# limitées, contenu identique et stable → e2e réel ET déterministe.
#
# Usage:  ./run-e2e.sh [N] [CONCURRENCY]     (défaut: 100 4)
set -uo pipefail

N="${1:-100}"
CONC="${2:-4}"
HERE="$(cd "$(dirname "$0")" && pwd)"
CFG="$HERE/../ghostchrome.mcp.json"
OUT="${E2E_OUT:-/tmp/ghostchrome-e2e}"
mkdir -p "$OUT"
: > "$OUT/results.tsv"

TOOLS="mcp__ghostchrome__navigate,mcp__ghostchrome__snapshot,mcp__ghostchrome__eval,mcp__ghostchrome__extract,mcp__ghostchrome__click,mcp__ghostchrome__type,mcp__ghostchrome__wait_for,mcp__ghostchrome__press,mcp__ghostchrome__tabs"

DOMAINS=(https://example.com https://example.org https://example.net)

# 8 templates : (question ; sous-chaîne attendue, insensible à la casse)
Q=(
  "the exact H1 heading text|Example Domain"
  "the visible text of the only link on the page|Learn more"
  "Does the page body contain the phrase 'documentation examples'? Reply yes or no|yes"
  "the page <title>|Example Domain"
  "the domain that the 'More information' link points to|iana"
  "the HTTP status code returned for the page|200"
  "how many <h1> elements are on the page (a number)|1"
  "the first word of the H1 heading|Example"
)

run_case() {
  local idx="$1"
  local dom="${DOMAINS[$(( (idx-1) % ${#DOMAINS[@]} ))]}"
  local tpl="${Q[$(( (idx-1) % ${#Q[@]} ))]}"
  local ask="${tpl%%|*}"
  local want="${tpl##*|}"
  local prompt="Use ONLY the ghostchrome MCP tools. Navigate to ${dom} and determine: ${ask}. Reply with ONLY the answer, no preamble."
  local t0 t1 dur raw res status
  t0=$(date +%s)
  raw=$(timeout 150 claude -p "$prompt" \
        --mcp-config "$CFG" --strict-mcp-config \
        --allowedTools "$TOOLS" \
        --permission-mode bypassPermissions --model haiku \
        --output-format json 2>>"$OUT/case-$idx.err")
  t1=$(date +%s); dur=$((t1 - t0))
  res=$(printf '%s' "$raw" | jq -r '.result // empty' 2>/dev/null)
  if printf '%s' "$res" | grep -qiF "$want"; then status=PASS; else status=FAIL; fi
  printf '%s\t%s\t%ss\t%s\t%s\t%q\n' "$idx" "$status" "$dur" "$dom" "$want" "$res" >> "$OUT/results.tsv"
  printf '[%3d] %-4s %2ss  %-20s want=%-18q got=%q\n' "$idx" "$status" "$dur" "$dom" "$want" "$res"
}
export -f run_case
export CFG OUT TOOLS
export DOMAINS_STR="${DOMAINS[*]}"
# re-export arrays for subshells via a serialized form
printf '%s\n' "${DOMAINS[@]}" > "$OUT/.domains"
printf '%s\n' "${Q[@]}" > "$OUT/.q"

# Portable fan-out: seq | xargs -P, re-sourcing arrays inside each worker.
seq 1 "$N" | xargs -P "$CONC" -I{} bash -c '
  mapfile -t DOMAINS < "'"$OUT"'/.domains"
  mapfile -t Q < "'"$OUT"'/.q"
  '"$(declare -f run_case)"'
  run_case {}
'

echo "================= SUMMARY ================="
pass=$(grep -c $'\tPASS\t' "$OUT/results.tsv")
fail=$(grep -c $'\tFAIL\t' "$OUT/results.tsv")
tot=$(wc -l < "$OUT/results.tsv")
echo "PASS=$pass  FAIL=$fail  TOTAL=$tot"
echo "results: $OUT/results.tsv"
