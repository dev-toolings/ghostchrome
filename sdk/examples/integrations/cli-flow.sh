#!/usr/bin/env bash
# cli-flow.sh : Voie B : ghostchrome piloté en CLI pure (aucun agent, aucun MCP).
#
# Équivalent direct d'un script Playwright : navigate → extract → interact →
# screenshot, mais en un seul binaire statique et une sortie taillée tokens.
# Le daemon Chrome transparent est auto-spawné au 1er appel et réutilisé ensuite
# (session "default"), donc l'état (cookies, onglets) persiste entre les lignes.
#
# Usage:  ./cli-flow.sh [url]         (défaut: https://example.com)
set -euo pipefail

URL="${1:-https://example.com}"
BIN="$(command -v ghostchrome || echo "$(cd "$(dirname "$0")/../../.." && pwd)/.local/bin/ghostchrome")"

echo "▶ 1. preview (santé page : statut + erreurs + réseau + DOM avec refs)"
"$BIN" preview "$URL"

echo
echo "▶ 2. navigate + extract niveau content (texte lisible)"
"$BIN" navigate "$URL" >/dev/null
"$BIN" extract --level content | head -20

echo
echo "▶ 3. exemple d'interaction : eval d'un fait DOM (escape hatch)"
"$BIN" eval 'document.querySelector("h1")?.textContent ?? "(no h1)"'

echo
echo "✔ Voie B OK : flux Playwright-like en CLI pure, zéro dépendance runtime."
