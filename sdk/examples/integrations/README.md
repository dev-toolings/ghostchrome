# Intégrer ghostchrome : 2 voies distinctes (remplacer Playwright)

ghostchrome remplace Playwright pour piloter Chrome, avec une sortie taillée pour
le budget tokens des agents LLM. Deux voies d'intégration **distinctes**, à choisir
selon *qui* pilote le navigateur.

```
                        ghostchrome (1 binaire Go statique)
                                     │
        ┌────────────────────────────┴────────────────────────────┐
        │                                                          │
╔═══════▼═════════ Voie A : MCP ═════════╗        ╔════════════════▼═══ Voie B : CLI ════════╗
║  L'AGENT pilote (Claude Code / Codex)   ║        ║  TON CODE pilote (scripts, tests, jobs)   ║
║  ┌───────────────────────────────────┐  ║        ║  ┌──────────────────────────────────────┐ ║
║  │ claude mcp add ghostchrome …      │  ║        ║  │ ghostchrome preview|navigate|extract…  │ ║
║  └──────────────┬────────────────────┘  ║        ║  └──────────────────┬───────────────────┘ ║
║                 │ JSON-RPC (stdio)       ║        ║                     │ argv → sortie compacte║
║        ┌────────▼────────┐               ║        ║          (aucun agent, aucun JSON-RPC)     ║
║        │ ghostchrome MCP │ 17 outils     ║        ║                                            ║
╚═════════════════│════════════════════════╝        ╚═════════════════════│═════════════════════╝
                  └───────────────┬────────────────────────────────────────┘
                                  │ CDP / Rod
               ┌──────────────────┴──────────────────┐
               │                                     │
      ┌────────▼─────────┐                  ┌────────▼─────────┐
      │ Chrome MCP       │                  │ Chrome CLI        │
      │ profil MCP       │                  │ session CLI       │
      └──────────────────┘                  └──────────────────┘
```

**Légende** : Voie A : serveur MCP, Chrome long-lived et refs `@1/@2` entre
appels. Voie B : CLI, session nommée réutilisée entre commandes. Les deux
partagent le moteur CDP, mais **pas** le processus Chrome, l’onglet actif ni les
références. Ne mélange jamais les deux voies dans un même workflow.

---

## Voie A : MCP (l'agent pilote)

Remplacement 1-pour-1 de `@playwright/mcp`. Installe d’abord le mode MCP avec
`ghostchrome setup --mode mcp`. Le serveur standalone est ensuite lancé par le
client avec un profil isolé (éphémère par défaut ; définis un nom unique via
`GHOSTCHROME_PROFILE` si tu veux conserver les cookies). Il ne se branche pas automatiquement sur
un `ghostchrome serve` lancé par la voie CLI. Vérifie que
`~/.ghostchrome/bin` est dans le `PATH` (ou remplace `ghostchrome-mcp` par ce
chemin installé stable dans la configuration du client).

```bash
# Claude Code (scope user → dispo partout)
claude mcp add ghostchrome -s user -- ghostchrome-mcp --stealth

# Codex
codex mcp add ghostchrome -- ghostchrome-mcp --stealth
```

Config portable réutilisable : [`ghostchrome.mcp.json`](./ghostchrome.mcp.json)
(consommable par `claude -p --mcp-config … --strict-mcp-config`).

Outils : `snapshot navigate click type select press hover drag fill_form upload
tabs wait_for eval screenshot back forward`. Si le serveur doit s’attacher à un
Chrome déjà lancé, fournis `GHOSTCHROME_CONNECT=auto` (ou un WebSocket CDP
explicite) et redémarre le client MCP ; `auto` peut créer une nouvelle target.

## Voie B : CLI pure (ton code pilote)

Aucun agent, aucun MCP. Le binaire fait tout, la sortie est compacte.
Démo runnable : [`cli-flow.sh`](./cli-flow.sh)

```bash
./cli-flow.sh https://example.com
```

---

## Validation e2e : Voie A isolée

[`e2e/run-e2e.sh`](./e2e/run-e2e.sh) lance N vrais runs `claude -p` (headless) qui
pilotent le navigateur via le MCP ghostchrome **isolé** (`--strict-mcp-config`),
avec assertion sur la sortie. Cibles RFC 2606 (`example.com/.org/.net`) : réelles,
jamais rate-limitées, contenu stable → e2e réel *et* déterministe.

```bash
./e2e/run-e2e.sh 100 4      # 100 cas, concurrence 4
```
