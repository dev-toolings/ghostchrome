# Audit d'architecture — ghostchrome

Date : 2026-09-08 · Périmètre : `main` @ bd13b2c · Lecture seule (aucun code modifié).

## 1. Chiffres mesurés

| Métrique | Valeur |
|---|---|
| LOC Go (src + test) | 58 436 |
| `engine/` (racine) | 17 640 src / 8 353 test · 133 fichiers · **281 symboles exportés** |
| `cmd/` (racine) | 16 864 src / 3 353 test · 91 fichiers · 113 commandes cobra · 442 fonctions |
| `engine/mcp/` | 1 881 src / 948 test · 19 tools |
| `engine/ai/` | 823 src / 140 test · 14 tools |
| `internal/ops/` | 390 src / 202 test · catalogue de 33 ops |
| Ratio test/src | engine 47 % · cmd 20 % · `packages/<site>` 0 % |
| Fichiers versionnés | 395 |

Le graphe d'import inter-paquets est **acyclique et correct** :
`main -> cmd -> {engine, engine/ai, engine/dashboard, engine/mcp, engine/policy,
engine/sites, engine/vault}`, `engine -> engine/policy`. Le problème n'est pas
la direction des dépendances, c'est leur **granularité**.

## 2. Constat — l'architecture réelle

```text
   SURFACES -- 4 implementations independantes du meme verbe metier
  +---------------+  +---------------+  +---------------+  +---------------+
  |  cmd/*.go     |  | cmd/agent.go  |  | engine/mcp/   |  | engine/ai/    |
  |  CLI cobra    |  | JSONL 1184 L  |  | tools.go      |  | tools.go      |
  |  117 fichiers |  | contrat SDK   |  | 19 tools      |  | 14 tools      |
  +-------+-------+  +-------+-------+  +-------+-------+  +-------+-------+
          |                  |                  |                  |
          +------------------+---------+--------+------------------+
                                       |
                                       v
       +-------------------------------+------------------------------+
       |  engine/   god package                                       |
       |  17 640 LOC . 133 fichiers . 281 symboles exportes           |
       |  browser session extract interact net stealth trace video    |
       +--------------------------------------------------------------+

       +------------------------+
       |  internal/ops          |   declaratif seulement : aucune des
       |  catalogue de 33 ops   |   4 surfaces ne le consomme
       +------------------------+
```

## 3. Findings

### F1 — Le contrat des SDK vit dans `package cmd` (bloquant)

La boucle JSONL, le protocole sur lequel `sdk/typescript` et `sdk/python` sont
typés, est implémentée dans `cmd/agent.go:328` (`agentSession.dispatch`), dans un
paquet `main`-adjacent, non importable.

Conséquence directe et documentée dans le code : `internal/ops/parity_test.go`
doit **recopier à la main** les 33 noms d'ops, avec ce commentaire :

> « JSONL surface (cmd/agent.go dispatch): separate package, no exported API;
>   names hardcoded here. »

Le garde-fou anti-drift est donc lui-même une duplication à maintenir. Un
développeur qui ajoute une op doit modifier 3 fichiers pour que le test échoue
correctement.

### F2 — Chaque verbe métier est écrit 4 fois

`click` existe en quatre implémentations non partagées :

| Surface | Fichier | Nature |
|---|---|---|
| CLI | `cmd/click.go:39-88` | résolution ref, retry, `emitMutationOutput` |
| JSONL | `cmd/agent.go:349` -> `opRef` (`:510-570`) | résolution ref, recovery, snapshot diff |
| MCP | `engine/mcp/tools.go:43` | **91 appels directs à `engine.*`** |
| AI | `engine/ai/tools.go:36` -> `cmd/ai.go:115 agentRunner.RunOp` | délègue à la boucle JSONL |

Seule la surface AI réutilise une autre surface. Les trois autres réimplémentent
recovery, retry sur ref périmée et calcul de mutation. C'est la source de tous
les bugs de parité passés.

### F3 — `internal/ops` est déclaratif, pas exécutable

`internal/ops/ops.go:11-14` l'admet explicitement :

> « IMPORTANT: this file is additive. The three surface files are NOT modified
>   to consume this registry — that refactor is a separate, future task. »

Le catalogue sert à générer `contracts/commands.json` et à alimenter un test.
Il ne **contraint** rien. Un « single source of truth » qu'aucun code de
production ne lit est de la documentation avec une extension `.go`.

### F4 — `engine/` est un god package

281 symboles exportés dans un seul paquet, contre 3 pour `engine/mcp`, 15 pour
`engine/ai`, 1 pour `engine/policy`. Il n'y a aucune frontière interne entre le
cycle de vie navigateur, l'extraction a11y, le fast-path HTTP (`fastfetch`,
`fetchapi`, `httpclient`), le stealth, l'enregistrement vidéo et le registre de
sessions. Tout est visible depuis tout ; rien n'est testable en isolation.

### F5 — `engine/mcp` et `engine/ai` sont mal placés

Un serveur JSON-RPC et un client LLM (Anthropic / OpenAI, `engine/ai/anthropic.go`)
ne sont pas des sous-modules d'un « moteur navigateur ». Ce sont des **surfaces**,
au même niveau que la CLI. Leur position actuelle sous `engine/` fait croire
qu'elles sont du domaine, et explique pourquoi `engine/mcp/tools.go` s'est autorisé
91 appels bruts à `engine.*`.

### F6 — La couche CLI porte de la logique métier

| Fichier | LOC | Devrait vivre où |
|---|---|---|
| `cmd/setup.go` | 1 461 | `internal/setup/` (installation, transports, doctor) |
| `cmd/agent.go` | 1 184 | `internal/surface/jsonl/` |
| `cmd/playwright_*.go` | ~3 200 | `internal/compat/playwright/` |
| `cmd/batch.go` | 530 | `internal/runtime/` |
| `cmd/browser_setup.go` | 344 | `internal/core/browser/` |

`cmd/` devrait contenir du parsing de flags et du formatage de sortie. Il contient
442 fonctions.

### F7 — `packages/` mélange deux concepts sans rapport

- `packages/npm/*` : 6 `package.json` de **distribution** (wrappers per-platform), versionnés.
- `packages/{leboncoin,linkedin,instagram,cars-listings,websearch}` : **recipes**
  métier, gitignorées, compilées via `-tags recipes`, 0 test.

Deux cycles de vie opposés sous le même nom.

### F8 — `docs/` est gitignoré

`.gitignore:65` contient `docs/`. Sur 11 fichiers présents dans `docs/`, **2 sont
versionnés** (`playwright-cli-parity.md`, `reliability-plan.md`, antérieurs à la
règle). `docs/architecture.md`, `docs/cli.md`, `docs/mcp.md`, `docs/anti-bot.md`
n'existent pas pour un nouvel arrivant ni en CI.

Corollaire : `docs/architecture.md` décrit une v2.0 et utilise du box-drawing
Unicode, contraire à la règle ASCII 7 bits du projet.

### F9 — Pas d'outillage monorepo

Aucun `package.json` racine, aucun workspace Bun. `sdk/typescript` a son propre
`bun.lock` isolé ; `packages/npm/cli*` a 6 manifestes sans racine commune. Le
`justfile` fait office de colle (`test-all`, `contract`, `e2e`) et c'est
aujourd'hui le seul lien réel entre les sous-projets.

### F10 — Règles `.gitignore` trop larges

`*.yaml` (ligne 58) ignore **tout** fichier YAML du dépôt, à tout niveau. Les
workflows CI survivent uniquement parce qu'ils sont en `.yml`. Toute config YAML
future sera silencieusement perdue.

## 4. Architecture cible

```text
   ADAPTATEURS DE SURFACE -- traduction seule, zero logique metier
  +---------------+  +---------------+  +---------------+  +---------------+
  | surface/cli   |  | surface/jsonl |  | surface/mcp   |  | surface/ai    |
  | cobra flags   |  | stdio ligne   |  | JSON-RPC      |  | tool_use LLM  |
  +-------+-------+  +-------+-------+  +-------+-------+  +-------+-------+
          |                  |                  |                  |
          +------------------+--------+---------+------------------+
                                      |  ops.Request
                                      v
       +------------------------------+---------------------------------+
       |  internal/runtime    ORCHESTRATION                             |
       |  Dispatch(ctx, ops.Request) (ops.Result, error)                |
       |  une seule impl par op . recovery . refs . policy              |
       +------------------------------+---------------------------------+
                                      |
                                      v
       +------------------------------+---------------------------------+
       |  internal/core       DOMAINE NAVIGATEUR                        |
       |  browser/  session/  extract/  interact/                       |
       |  observe/  net/  stealth/  storage/                            |
       +------------------------------+---------------------------------+
                                      |  CDP / HTTP
                                      v
                              +-------+-------+
                              |    Chrome     |
                              +---------------+
```

Règle unique qui porte tout le reste :
**une op = une implémentation dans `internal/runtime`**. Les quatre surfaces
deviennent des traducteurs (`args -> ops.Request`, `ops.Result -> sortie`). Elles
n'appellent plus jamais `internal/core` directement.

### Le catalogue devient exécutable

```text
                +---------------------------+
                |  internal/ops             |
                |  Catalog() -- 33 ops      |
                |  go generate  =>  codegen |
                +-------------+-------------+
                              |
   +---------+---------+------+------+------------------+
   |         |         |             |                  |
   v         v         v             v                  v
+--+--+   +--+--+   +--+--+   +------+----+   +---------+--------+
| cli |   |jsonl|   | mcp |   |    ai     |   |  contracts/      |
| gen |   | gen |   | gen |   |    gen    |   |  commands.json   |
+-----+   +-----+   +-----+   +-----------+   +--------+---------+
                                                       |
                                            +----------+----------+
                                            |                     |
                                            v                     v
                                     +------+-------+     +-------+------+
                                     |  sdk/ts      |     |  sdk/python  |
                                     +--------------+     +--------------+
```

Le `parity_test.go` avec ses listes en dur disparaît : la parité devient
structurelle, plus testée.

### Arborescence cible

```text
ghostchrome/
  cmd/
    ghostchrome/          main CLI          (~15 lignes)
    ghostchrome-mcp/      main MCP          (~15 lignes)
  internal/
    ops/                  catalogue + ops.Request/Result + generateurs
    core/                 DOMAINE (ex engine/)
      browser/            lifecycle, launcher, provider, profile, prewarm
      session/            registry, daemon, state, contexts, page_session
      extract/            a11y tree, refs, SSR/RSC, DOM fallback, diff
      interact/           click, type, mouse, touch, drag, human, locator
      observe/            errors, console, network, HAR, trace, recorder, video
      net/                fastfetch, fetchapi, httpclient, intercept, proxypool
      stealth/            evasion, antibot_blocker, cookies banner
      storage/            cookies, localStorage, vault
    runtime/              Dispatch : orchestration, recovery, mutation, policy
    surface/
      cli/                cobra : flags -> Request, Result -> stdout
      jsonl/              boucle stdio        (ex cmd/agent.go)
      mcp/                serveur MCP         (ex engine/mcp/)
      ai/                 boucle LLM + providers (ex engine/ai/)
    setup/                install, doctor, skills, instructions (ex cmd/setup.go)
    compat/playwright/    parite Playwright CLI (ex cmd/playwright_*.go)
  contracts/              commands.json (genere, versionne)
  sdk/typescript/         client JSONL type
  sdk/python/             client JSONL type
  dist/npm/               wrappers de distribution (ex packages/npm/)
  recipes/                scrapers gitignores, build tag `recipes`
  docs/                   VERSIONNE
  benchmark/
```

## 5. Plan de migration

Cinq phases, chacune livrable seule, aucune ne casse la CLI publique.

| # | Phase | Contenu | Critère d'acceptation |
|---|---|---|---|
| 0 | Hygiène | retirer `docs/` et `*.yaml` de `.gitignore`, versionner `docs/`, réécrire `docs/architecture.md` en ASCII 7 bits, `package.json` racine avec workspaces Bun (`sdk/typescript`, `dist/npm/*`) | `git ls-files docs \| wc -l` >= 11 ; `bun install` à la racine |
| 1 | Extraire le domaine | `engine/` -> `internal/core/<sous-domaine>/`, un déplacement par sous-domaine, `git mv` + ajustement d'imports uniquement | `go build ./... && go test -short ./...` vert à chaque commit ; exports de `internal/core` (racine) = 0 |
| 2 | Créer `internal/runtime` | remonter `agentSession` de `cmd/agent.go` en `runtime.Dispatch`, y concentrer recovery / refs / mutation ; `cmd/agent.go` devient un adaptateur stdio | `internal/runtime` importable ; `parity_test.go` lit les noms d'ops au runtime au lieu de la liste en dur |
| 3 | Rebrancher les surfaces | `surface/mcp` et `surface/cli` appellent `runtime.Dispatch` ; les 91 `engine.*` de `tools.go` tombent à 0 ; `surface/ai` garde son mapping tool_use | `grep -c 'core\.' internal/surface/**/*.go` == 0 ; conformance + e2e verts |
| 4 | Catalogue exécutable | `go generate` émet les schémas MCP, les specs AI et les flags CLI depuis `ops.Catalog()` | ajouter une op = éditer `ops.go` seul ; `parity_test.go` supprimé |
| 5 | Séparer distribution / recipes | `packages/npm` -> `dist/npm`, `packages/<site>` -> `recipes/`, mettre à jour `.gitignore` et le tag de build | `go build -tags recipes ./...` vert ; `packages/` n'existe plus |

**Ne pas faire** : découper en plusieurs modules Go. Un seul `go.mod` reste le
bon choix : le binaire statique unique est la promesse produit, et le graphe
d'import est déjà acyclique.

## 6. Risques

| Risque | Mitigation |
|---|---|
| Phase 1 = diff énorme, revue impossible | un commit par sous-domaine, `git mv` pur, zéro changement de corps de fonction |
| Régression de parité pendant la phase 3 | garder `parity_test.go` actif jusqu'à la fin de la phase 4 |
| `recipes/` gitignoré : les renommages ne sont pas rejouables en CI | documenter le mapping dans `docs/` avant la phase 5 |
| `cmd/` couvert à 20 % : les extractions ne sont pas filetées par des tests | écrire les tests de `runtime.Dispatch` **pendant** la phase 2, pas après |
