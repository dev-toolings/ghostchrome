# ghostchrome, phase 1 domain extraction: interfaces to extract, 1c decision, follow-ups

Companion to `docs/architecture-audit.md`. Measured at `2808f29`, right after the
phase 1 domain extraction. It answers one question: what has to happen for the
remaining 54-file `engine` core to be split, and in which order.

The short answer is section 7. The evidence is sections 0 to 4.

---

## 0. How these numbers were produced

Not from a regex heuristic. I rebuilt the file-level symbol graph of package
`engine` with `go/ast`:

- declaration ownership taken from the AST (`FuncDecl`, `TypeSpec`, `ValueSpec`),
  so grouped `var` / `const` blocks and methods are attributed correctly;
- identifier uses taken from the AST with `SelectorExpr.Sel` and struct-literal
  keys excluded, so `x.Foo` and `Foo{Bar: 1}` do not produce phantom edges, and
  nothing inside strings or comments is counted;
- `_unix` / `_windows` build-tag siblings merged into one logical node, because
  they declare the same symbol set and a naive owner map lets the alphabetically
  last file win, which fabricates "pure roots".

The tool is at `/tmp/gcanalyze/` (`go run . <dir>`). It prints pure roots, pure
leaves, the movable closure, hub symbols and cyclic group pairs.

### State of `engine/` after phase 1

| metric | value |
|---|---|
| non-test files | 54 |
| source LOC | 14 432 |
| mutually entangled files (in-degree > 0 and out-degree > 0) | 34 (12 366 LOC) |
| pure roots (nothing in the package references them) | 7 (1 125 LOC) |
| pure leaves (they reference nothing in the package) | 10 (941 LOC) |
| fully isolated (both) | 3 |

Pure roots still in place, with what blocks each one:

| file | LOC | blocked by |
|---|---|---|
| `video_runtime.go` | 265 | `connectRodBrowser`, unexported in `browser.go` |
| `fetchapi.go` | 214 | `fastFetchUA`, `readResponseBody`, unexported in `fastfetch.go` |
| `profiles.go` | 177 | `validateSessionName`, unexported in `session_registry.go` |
| `touch.go` | 139 | `setTouchEmulation` (`emulate.go`), `settleAfterAction` (`interactor.go`) |
| `drop.go` | 124 | `drop_target_test.go` uses unexported `parsePlaywrightLocator`, `dataURL` |
| `nav_wait.go` | 111 | `conformance_test.go` uses unexported `lifecycleHit` |
| `drag.go` | 95 | `settleAfterAction`, unexported in `interactor.go` |

Every one of these is one exported symbol away from moving. That is the shape of
the problem: the core is not held together by architecture, it is held together
by a handful of unexported helpers and two misplaced value types.

### Cyclic pairs

Groups used for the group-level graph:

```
browser   browser runtime protocol emulate initscript popup_watch page_profile
          profile discover
session   session_registry session_state session_spawn_PLATFORM page_session
          context_registry profile_lock profiles persistent_observer
          persistent_observer_lock_PLATFORM persistent_observer_replace_PLATFORM
navigate  navigator wait locator locator_wait nav_wait
extract   extractor extractor_domfallback ssr_extract rsc_extract snapshot_diff
          format preview
interact  interactor drag drop touch human
observe   errors observer event_hub capture mutation video_runtime
net       fastfetch fastfetch_tls fetchapi httpclient intercept intercept_rules
          network_tracker
stealth   stealth evasion
```

**16 cyclic pairs out of 28 possible.** Correction to my first report, which said
17: the exact count is 16.

Also a correction to the original brief: **there is no `net <-> browser` cycle**
in this measurement. The cycles on the network side are `net <-> extract` and
`net <-> observe`. The likely cause of the discrepancy is the group assignment of
`intercept.go` and `fetchapi.go`, which differ between the regex map and this
one, not a disagreement about the code.

| pair | forward | reverse |
|---|---|---|
| `browser <-> session` | 15 | 11 |
| `browser <-> observe` | 13 | 3 |
| `interact <-> session` | 6 | 10 |
| `browser <-> interact` | 5 | 9 |
| `extract <-> observe` | 2 | 5 |
| `extract <-> session` | 1 | 5 |
| `interact <-> navigate` | 2 | 5 |
| `extract <-> net` | 2 | 4 |
| `browser <-> navigate` | 2 | 4 |
| `observe <-> session` | 3 | 9 |
| `browser <-> stealth` | 1 | 3 |
| `extract <-> interact` | 2 | 3 |
| `extract <-> navigate` | 2 | 2 |
| `net <-> observe` | 1 | 2 |
| `browser <-> extract` | 1 | 2 |
| `observe <-> stealth` | 1 | 1 |

### Hub symbols (referenced by four or more distinct files)

| symbol | kind | owned by | files | referencing files |
|---|---|---|---|---|
| `Browser` | type | `browser.go` | 13 | context_registry discover event_hub initscript interactor intercept mutation page_session persistent_observer popup_watch runtime stealth wait |
| `PageSnapshot` | type | `session_state.go` | 10 | browser drag drop interactor locator locator_wait mutation page_session popup_watch wait |
| `ObserverEvent` | type | `observer.go` | 5 | browser errors event_hub persistent_observer session_state |
| `StartFileChooserIntercept` | func | `interactor.go` | 4 | extractor navigator page_session popup_watch |
| `ExtractionResult` | type | `extractor.go` | 4 | browser mutation preview session_state |

---

## 1. Tier 1: the two hubs

Nothing else moves until these do.

### 1.1 `PageSnapshot`, and it is not an interface problem

**Hub type:** `PageSnapshot`
**Owned by:** `engine/session_state.go`
**Referenced by 10 files:** `browser.go` `drag.go` `drop.go` `interactor.go`
`locator.go` `locator_wait.go` `mutation.go` `page_session.go` `popup_watch.go`
`wait.go`

`PageSnapshot` is the ref table: the mapping that turns `@1`, `@2` into live
element handles. `interact`, `navigate` and `observe` all read it; `session`
writes it. Because it lives in `session_state.go`, **every consumer of a ref is
forced to import the session package**. That single placement decision is
responsible for more cross-group edges than any interface in this codebase.

**This needs no interface extraction and no design work.** It is a value type in
the wrong package.

**Inversion:** move the type downward, not the dependency sideways. Create a leaf
package that imports nothing local:

```
internal/core/snapshot
    PageSnapshot
    RefSnapshot
    snapshotFromResult   (exported on move: SnapshotFromResult)
```

Everyone then imports downward into `snapshot`. `session` keeps the logic that
builds and stores snapshots; it no longer owns the type that everybody needs.

**Concrete file pairs unblocked:**

- `session_state.go <-> interactor.go`
- `session_state.go <-> browser.go`
- `session_state.go <-> mutation.go`
- `session_state.go <-> wait.go`
- `session_state.go <-> locator.go`, `session_state.go <-> locator_wait.go`
- `session_state.go <-> popup_watch.go`
- `session_state.go <-> page_session.go`

At group level it empties half of `interact <-> session` (`PageSnapshot`,
`BuildSnapshot`, `RefSnapshot` out of 6 symbols in the `interact -> session`
direction) and the whole `observe -> session` direction (all 3 symbols:
`BuildSnapshot`, `PageSnapshot`, `RefSnapshot`).

**Cost:** a `git mv`, a package clause, and qualifier fixes. It is the cheapest
high-value move available in this refactor, and it should be step 1 of the
execution plan.

### 1.2 `Browser`, the god object

**Hub type:** `Browser`
**Owned by:** `engine/browser.go`
**Referenced by 13 files:** `context_registry.go` `discover.go` `event_hub.go`
`initscript.go` `interactor.go` `intercept.go` `mutation.go` `page_session.go`
`persistent_observer.go` `popup_watch.go` `runtime.go` `stealth.go` `wait.go`

Every group reaches for the concrete `*Browser`. Measured widths:

| edge | symbols | names |
|---|---|---|
| `browser -> session` | 15 | AcquireContext BrowserTraceState EmulationState ErrAmbiguousRef PageSession PageSnapshot VideoState appendContinuousClear continuousObserverLogPath loadSessionState readContinuousEvents saveSessionState sessionState sessionStatePath snapshotFromResult |
| `session -> browser` | 11 | Browser DefaultActionTimeout Device DiscoverCDP ProbeCDPEndpoint ResolveProfileDir Runtime connectRodBrowser setClickDownloadHint setClickNavHint setClickPopupHint |
| `browser -> observe` | 13 | CapturedEntry EventHub HubForPage KindConsole KindError KindNet KindPage NewObserver Observer ObserverEvent ObserverOpts StartEventHub stopBrowserEventHubs |
| `observe -> browser` | 3 | Browser NavWaitTimeout connectRodBrowser |
| `browser -> interact` | 5 | ErrStaleRef RefMayOpenPopup StartFileChooserIntercept forgetBrowserEventState pageSessionKey |
| `interact -> browser` | 9 | ApplyInitScripts Browser ClickNavHint DefaultActionTimeout WaitForClickDownload WaitForClickNavigation installedPageScripts setClickNavHint setTouchEmulation |
| `browser -> navigate` | 2 | ActivePolicy WaitForPage |
| `navigate -> browser` | 4 | ApplyInitScripts Browser NavWaitTimeout navTimeout |
| `browser -> stealth` | 1 | dedupeStrings |
| `stealth -> browser` | 3 | ApplyTimezone Browser chromeMajor |

**Inversion:** consumers declare, **in their own package**, the narrow interface
they actually use. `browser` then imports nobody. The five shapes present in the
measured call sites:

```go
// consumed by interact, observe, session, extract
type PageProvider interface {
    Page() (*rod.Page, error)
    RodBrowser() *rod.Browser
}

// consumed by session (profile dir resolution, lock files)
type ProfileHolder interface {
    ResolveProfileDir() string
}

// consumed by observe (video_runtime) and session (session_registry)
type Connector interface {
    Connect(ctx context.Context) (*rod.Browser, error)   // today: connectRodBrowser
}

// consumed by interact and navigate
type ScriptHost interface {
    ApplyInitScripts(page *rod.Page) error
}

// consumed by session and observe
type Closer interface {
    Close()
}
```

These shapes are read off the call sites, not designed in the abstract. Confirm
the exact method sets against `browser.go` when implementing; the count of
methods per interface is what matters, and it is one or two everywhere.

Note that `connectRodBrowser` is unexported today, which is precisely why
`video_runtime.go` cannot leave the core. `Connector` is the interface that frees
it.

**Concrete file pairs unblocked:**

- `browser.go <-> interactor.go`
- `browser.go <-> observer.go` and `browser.go <-> event_hub.go`
- `browser.go <-> session_registry.go`, `browser.go <-> page_session.go`,
  `browser.go <-> context_registry.go`, `browser.go <-> persistent_observer.go`
- `browser.go <-> stealth.go`
- `browser.go <-> navigator.go`
- `browser.go <-> intercept.go`, `browser.go <-> popup_watch.go`,
  `browser.go <-> mutation.go`, `browser.go <-> initscript.go`,
  `browser.go <-> runtime.go`, `browser.go <-> wait.go`,
  `browser.go <-> discover.go`

Group level: 5 of the 16 cyclic pairs (`browser <-> session`,
`browser <-> observe`, `browser <-> interact`, `browser <-> navigate`,
`browser <-> stealth`).

**Side effect worth planning for:** exporting `connectRodBrowser` as part of
`Connector` also unblocks `video_runtime.go` (265 LOC pure root) with no further
work.

---

## 2. Tier 2: three inversions, one interface each

### 2.1 `ObserverEvent` / `Observer` / `ObserverKind` / `ObserverOpts`

**Hub type:** `ObserverEvent` (and its siblings `Observer`, `ObserverKind`,
`ObserverOpts`)
**Owned by:** `engine/observer.go`
**Referenced by 5 files:** `browser.go` `errors.go` `event_hub.go`
`persistent_observer.go` `session_state.go`

This cycle is lopsided: `browser -> observe` is 13 symbols wide, `observe ->
browser` is only 3 (`Browser`, `NavWaitTimeout`, `connectRodBrowser`). The
dependency that should exist is `browser -> observe`. The weak reverse edge is
the accident.

**Inversion:** `observe` must stop knowing about `*Browser`.

```go
// declared in observe, satisfied by browser and by any page holder
type Target interface {
    RodPage() *rod.Page
}

// declared in observe, implemented by whoever wants the events
type EventSink interface {
    Emit(ev ObserverEvent)
}
```

`browser` registers its hubs by handing an `EventSink` to the observer instead of
the observer reaching back for a `*Browser`. Direction becomes `browser ->
observe`, one way.

**Concrete file pairs unblocked:**

- `browser.go <-> observer.go`
- `event_hub.go <-> browser.go`
- `persistent_observer.go <-> browser.go`
- `session_state.go <-> observer.go`

It also resolves the remainder of `observe <-> session`: the 9 symbols in the
`session -> observe` direction (`CaptureMutation`, `CapturedEntry`,
`KindConsole`, `KindError`, `NewObserver`, `Observer`, `ObserverKind`,
`ObserverOpts`, `ObserverEvent`) become a plain downward import once
`PageSnapshot` has moved (tier 1.1) and `Target` exists.

### 2.2 `StartFileChooserIntercept`

**Hub symbol:** `StartFileChooserIntercept` (a function, not a type)
**Owned by:** `engine/interactor.go`
**Referenced by 4 files:** `extractor.go` `navigator.go` `page_session.go`
`popup_watch.go`

A single function dragging `extract`, `navigate` and `session` into `interact`.
Nothing about it is interaction logic: it arms a CDP domain and installs a
handler. It sits in `interactor.go` for historical reasons only.

**Inversion:** no interface. Relocate it, either into `browser` (it is
session-scoped CDP arming, same family as `prewarm`) or into a leaf package
`internal/core/filechooser`. The leaf is preferable: it keeps `browser` from
growing and it is importable from all four consumers without any new edge.

**Concrete file pairs unblocked:**

- `extractor.go <-> interactor.go`, which **removes `extract <-> interact`
  entirely**: that pair rests on exactly two symbols,
  `StartFileChooserIntercept` and `intPtr`, and `intPtr` is a two-line helper
  that belongs in a utility leaf anyway.
- `navigator.go <-> interactor.go`, reduced from 5 symbols to 4
  (`EnableDialogAutoAccept`, `ErrStaleRef`, `ResolveRef`, `parseRef` remain, and
  three of those four are ref-resolution symbols addressed in section 4).
- `popup_watch.go <-> interactor.go`
- `page_session.go <-> interactor.go`

### 2.3 `ExtractionResult` / `ExtractedNode`

**Hub type:** `ExtractionResult` (with `ExtractedNode`, `ElementBox`)
**Owned by:** `engine/extractor.go`
**Referenced by 4 files:** `browser.go` `mutation.go` `preview.go`
`session_state.go`

Same diagnosis as `PageSnapshot`: a result value type stuck in the package that
produces it, needed by `observe` (`DiffRefs`, `SnapshotDiff`) and by `session`.

**Inversion:** move the **result types** into the same leaf package as
`PageSnapshot` (`internal/core/snapshot`, or a sibling `internal/core/domtypes`
if you prefer to keep refs and DOM nodes separate). Leave the **extraction
logic** in `extract`.

Note that `internal/core/overlay` already imports `engine` solely for
`ElementBox`, `ExtractedNode` and `ExtractionResult`. Moving these types makes
`overlay` a leaf too, at zero extra cost.

**Concrete file pairs unblocked:**

- `extractor.go <-> session_state.go`
- `extractor.go <-> mutation.go`
- `extractor.go <-> browser.go`, which removes `browser <-> extract`: the
  `browser -> extract` direction rests on `ExtractionResult` alone
- `extractor.go <-> preview.go`
- the `observe -> extract` direction of `extract <-> observe`
  (`DiffRefs`, `Extract`, `ExtractionResult`, `LevelSkeleton`, `SnapshotDiff`)

---

## 3. Tier 3: thin cycles, one or two symbols each

These are not architecture. They are single helpers and value types that landed
in the wrong file. Fix them while passing through; each is minutes of work and
each removes a real edge from the graph.

| cyclic file pair | symbols holding it | fix |
|---|---|---|
| `browser.go <-> stealth.go` | `dedupeStrings` (b->s); `ApplyTimezone`, `chromeMajor` (s->b) | `dedupeStrings` into a `stringsutil` leaf |
| `observer.go <-> stealth.go` | `EvadeRuntimeEnable` (o->s); `firstNonEmpty` (s->o) | `firstNonEmpty` into the same leaf |
| `browser.go <-> extractor.go` | `ProfileHuman`, `RenderProfile` (e->b) | these are `page_profile.go` concerns, not extraction; regroup `page_profile` out of the `browser` group |
| `network_tracker.go <-> observer.go` | `ErrorEntry` (n->o); `newRequestTracker`, `requestTracker` (o->n) | `ErrorEntry` into the leaf types package |
| `extractor.go <-> network_tracker.go` | `newRequestTracker`, `requestTracker` (e->n); `ExtractSSRPayloads`, `NetworkEntry`, `SSRPayload`, `SourceNextData` (n->e) | `NetworkEntry`, `SSRPayload`, `SourceNextData` into the leaf types package |
| `extractor.go <-> navigator.go` | `Navigate`, `PageInfo` (e->n); `InternalRef`, `axValueStr` (n->e) | `InternalRef` and `axValueStr` into the leaf types package |

`NetworkEntry` in particular is already load-bearing outside the core:
`internal/core/artifact.BuildHAR` takes an `engine.NetworkEntry`, which is the
only reason `artifact` imports `engine` at all. Moving it makes `artifact` a leaf.

---

## 4. What remains after tiers 1 to 3, and why it is not a phase 1 problem

One cycle survives: **`interact <-> session`**.

| direction | symbols |
|---|---|
| `interact -> session` (6) | `BuildSnapshot` `EnsureActionable` `PageSnapshot` `ResolveRefSemantic` `snapshotRefNum` `withSemanticRetry` |
| `session -> interact` (10) | `ClickElementWithButton` `ClickRef` `ErrStaleRef` `HoverElement` `SelectOption` `SetCheckedRef` `StartFileChooserIntercept` `TypeElement` `dblClickElementWithButton` `parseRef` |

Tier 1.1 removes `PageSnapshot` and `BuildSnapshot`; tier 2.2 removes
`StartFileChooserIntercept`. What is left is the genuine domain question:
**who owns ref resolution**.

My reading of the code is that `ResolveRefSemantic`, `withSemanticRetry`,
`EnsureActionable`, `parseRef` and `snapshotRefNum` are neither interaction nor
session state. They are orchestration: resolve a ref, retry on staleness,
re-extract, retry the action. That is the loop `cmd/agent.go` implements today,
and the audit already schedules it for `internal/runtime` in phase 2.

So this cycle should be resolved by **moving code out of the core**, not by
extracting an interface inside it. It is phase 2's problem, and that has a direct
consequence for the naming decision in section 5.

---

## 5. Step 1c: skipped, and the argument

I did not perform the `engine -> internal/core` move with package clause
`engine -> core`. `engine/` still exists on disk. The brief allowed this outcome
if argued; here is the argument.

### 5.1 The literal instruction contradicts the audit's own acceptance criterion

Renaming package `engine` to `core` at path `internal/core` places a package of
roughly 230 exported symbols **at the root of `internal/core`**. The phase 1
acceptance criterion in `docs/architecture-audit.md` says, verbatim:

> exports de `internal/core` (racine) = 0

It would also make `internal/core` simultaneously a Go package and the parent
namespace of the ten packages just created under it. That is legal Go and bad
structure, and it moves the tree away from the target arborescence rather than
toward it.

### 5.2 The variant that avoids that trap buys a name the next phases delete

`engine -> internal/core/engine`, keeping the package clause `engine`, does
satisfy the criterion. Its cost is genuinely low: no qualifier churn at all,
only the import path string changes, in roughly 150 files, and the compiler
verifies every one.

But it introduces `internal/core/engine`, a name that phases 2 to 4 delete, and
it rewrites the same import lines twice: once now, once when the core is
actually split into `browser/`, `session/`, `extract/`.

### 5.3 The code argument: the right target name is not yet known

Section 4 is the substantive reason. The remaining core may not deserve to be one
package called `core` at all:

- the `interact <-> session` cycle points at `internal/runtime`, meaning a
  meaningful chunk of what is in `engine/` today leaves the domain layer
  entirely in phase 2;
- tiers 1.1, 2.3 and 3 all point at the same conclusion, that a **leaf types
  package** must exist below whatever the core becomes.

Fixing the name now means fixing it before knowing whether the target is
`internal/core` as a package, or directly
`internal/core/{browser,session,extract,snapshot}` with nothing at the root.

### 5.4 Skipping costs nothing that matters

The rename changes **zero edges** in the dependency graph. The graph is already
acyclic and already one-way after phase 1:

```
engine                       -> internal/core/media, internal/core/policy
internal/core/{antibot, artifact, coretest, feedback, inspect, interact,
               overlay, provider}                        -> engine
internal/core/{dashboard, media, pagesetup, policy, proxy, sites, storage,
               vault}                                    -> (nothing local)
cmd, engine/mcp, engine/ai                               -> engine + the above
```

The coherence that matters is acquired. Only the label is missing.

One more consequence of the brief worth recording: because `engine/mcp` and
`engine/ai` stay in place until phase 3, `engine/` becomes a husk holding two
surface packages plus `testdata/` under **either** variant of 1c. That is not an
argument for or against, but it means 1c does not actually make `engine/`
disappear today.

### 5.5 Recommendation

Fold the rename into the **end of phase 2**, once `internal/runtime` exists and
the core is being split for real. One import rewrite instead of two, and by then
the correct target name is known.

---

## 6. `github.com/MakFly/ghostchrome`: tracked or ignored

### 6.1 What I observed

`go build -tags recipes ./...` fails, and failed identically on the pristine tree
before phase 1 (verified with `git stash -u`). Seven files are named in the
compiler output, all importing `github.com/MakFly/ghostchrome/...` while `go.mod`
declares `github.com/dev-toolings/ghostchrome`:

```
cmd/cars_listings.go:6   cmd/websearch_mcp.go:10   cmd/instagram.go:7
cmd/leboncoin.go:7       cmd/linkedin.go:7         cmd/websearch.go:7
packages/cars-listings/site.go:6, autoscout24.go:11, auto_gestion.go:10
```

These are gitignored, on three concordant grounds:

1. `git status --short` was empty before and after phase 1 while the files exist
   on disk;
2. `git stash -u` remises untracked files but **not** ignored ones; these files
   survived the stash and produced the identical seven errors, so they are
   ignored, not merely untracked;
3. `CLAUDE.md` states it: "`packages/<site>/` + `cmd/<site>.go` are gitignored,
   kept on disk, never committed".

### 6.2 On your finding in `docs/README.md:53` and `docs/mcp.md:23`

I cannot confirm it from my own observation, and I am not running a command to
check, per your instruction. **Take your measurement as authoritative over mine
on this point.** My evidence covered only the build-breaking occurrences, which
are the gitignored recipes.

I want to be explicit that I never ran a repo-wide search for `MakFly` over
tracked files. I flagged that gap in my first report and suggested exactly the
check you ran. Your result fills it, and it does not contradict anything I
measured: `go install github.com/MakFly/ghostchrome@latest` in a documentation
file is not something a Go build would ever surface.

There is a coherent explanation for why those two lines are new to the tracked
set: `docs/` was gitignored until very recently. `docs/architecture-audit.md` finding
F8 records that `.gitignore:65` contained `docs/` and that only 2 of 11 doc files
were versioned. Commit `19525a0` ("docs: version the documentation directory")
brought the rest in. Those two stale install commands were therefore invisible to
CI and to any tracked-file search until that commit landed.

### 6.3 Other tracked occurrences

I saw none, and I cannot establish absence, having never searched for the string.

### 6.4 Revised verdict

Split, not "phase 5":

- **Today's problem:** `docs/README.md:53` and `docs/mcp.md:23` publish an install
  command that cannot resolve. Now that `docs/` is tracked, this is user-facing
  and one-line fixable. It does not depend on any phase.
- **Phase 5's problem:** the module path drift inside the gitignored recipes,
  which breaks `go build -tags recipes ./...`. Untouchable from CI by
  construction, and entangled with the `packages/` split the audit schedules for
  phase 5.

### 6.5 Related doc drift I did observe

While grepping non-Go references during phase 1, I saw these tracked files still
describing the pre-`771066c` layout, with `engine/policy/`, `engine/vault/`,
`engine/provider/`, `engine/dashboard/`, `engine/sites/` at their old paths:

- `docs/architecture.md:72,154,155,156,157,158,235,240`
- `.referential/project-map.md:45`
- `docs/architecture-audit.md:19,20,95` (historical by nature, arguably fine as-is)

I deliberately left all of them alone: `yamato-hygiene` was working `docs/` at the
time and `docs/architecture.md` additionally needs the Unicode box-drawing to
ASCII conversion that F8 calls for. Flagging, not fixing.

---

## 7. Ordered execution plan for the core split

Same method as phase 1: one sub-step per commit, `git mv` for every move, no
function body edited except to fix an import path or a package qualifier.

**Green gate between every step, without exception:**

```
CGO_ENABLED=0 go build ./...
go vet ./...
go test -short -count=1 ./...
gofmt -l ./cmd ./engine ./internal        # must print nothing
```

Plus, once per tier rather than per step:

```
go list -f '{{.ImportPath}} {{join .Imports " "}}' ./... | grep dev-toolings
```

to confirm the graph is still acyclic and that no new back edge appeared.

`go build -tags recipes ./...` stays red for the reason in section 6. Do not treat
it as a regression, and do not "fix" it by touching the gitignored recipes.

### Step 1: `internal/core/snapshot`, the leaf types package

**This is the cheapest high-value move available and it must come first.** It is
a `git mv` with no interface design whatsoever.

Move into a package that imports nothing local:

- from `session_state.go`: `PageSnapshot`, `RefSnapshot`, `snapshotFromResult`
- from `extractor.go`: `ExtractionResult`, `ExtractedNode`, `ElementBox`
- from `network_tracker.go` / `preview.go`: `NetworkEntry`
- from `errors.go`: `ErrorEntry`
- from `ssr_extract.go`: `SSRPayload`, `SourceNextData`
- from `extractor.go` / `navigator.go`: `InternalRef`, `axValueStr`
- helpers into a sibling `stringsutil` leaf: `dedupeStrings`, `firstNonEmpty`,
  `intPtr`

**Clears on its own:** the `session_state.go` half of tier 1.1 (7 file pairs),
`browser <-> extract`, `net <-> observe`, `extract <-> net`,
`extract <-> navigate`, `observe <-> stealth`, `browser <-> stealth`, and the
`observe -> session` direction. Roughly 6 of the 16 group-level cyclic pairs.

**Bonus, measurable immediately:** `internal/core/overlay` and
`internal/core/artifact` stop importing `engine` and become leaves.

**Extra green gate for this step:** confirm those two packages now show `-` in the
import graph listing.

### Step 2: the `Browser` inversion

Introduce `PageProvider`, `ProfileHolder`, `Connector`, `ScriptHost`, `Closer` in
the consuming packages, in that order of impact. One commit per interface, not
one commit for all five.

Export `connectRodBrowser` as part of `Connector`, and in the **same** commit move
`video_runtime.go` (265 LOC) out of the core, since that is the only thing
blocking it.

**Clears:** 5 group-level pairs (`browser <->` session, observe, interact,
navigate, stealth) and 13 concrete file pairs.

**Extra green gate:** `grep -c 'engine\.Browser' internal/core/**/*.go` should be
strictly decreasing across the five commits.

### Step 3: the `Observer` inversion and the `StartFileChooserIntercept` relocation

Two independent commits, either order.

- `Target` and `EventSink` in `observe`; `browser` pushes instead of `observe`
  pulling.
- `StartFileChooserIntercept` into `internal/core/filechooser`.

**Clears:** `browser <-> observe` residue, `extract <-> interact` entirely,
`observe <-> session` residue, and shrinks `interact <-> navigate`.

### Step 4: harvest the freed pure roots

After steps 1 to 3, re-run `/tmp/gcanalyze` and move what has become movable. The
known candidates and their unblocking condition:

| file | LOC | unblocked by |
|---|---|---|
| `video_runtime.go` | 265 | step 2 (`Connector`) |
| `touch.go` | 139 | export `setTouchEmulation`, `settleAfterAction` |
| `drag.go` | 95 | export `settleAfterAction` |
| `fetchapi.go` | 214 | export `fastFetchUA`, `readResponseBody` |
| `profiles.go` + `session_registry.go` + `discover.go` + `profile_lock.go` + `session_spawn_*` | 1 064 | export `lockContinuousLog` from `persistent_observer_lock_{unix,windows}.go` |
| `drop.go` | 124 | split `drop_target_test.go`: the locator tests stay, the drop tests move |
| `nav_wait.go` | 111 | move the `lifecycleHit` assertions out of `conformance_test.go` |

The 1 064 LOC session cluster is the single largest remaining win and it hinges on
**one** unexported symbol.

### Step 5: `internal/runtime`, then the rename

Only here does `interact <-> session` get resolved, by lifting ref resolution
(`ResolveRefSemantic`, `withSemanticRetry`, `EnsureActionable`, `parseRef`,
`snapshotRefNum`) out of the core into `internal/runtime`, alongside the JSONL
dispatch loop from `cmd/agent.go`.

`internal/core/feedback` (extracted in phase 1) folds into `internal/runtime` at
this point; that was anticipated in its commit message.

**Do the `engine` rename last, in this step**, when the target names are known
(section 5). One import rewrite, not two.

### Ordering constraint to respect

Step 1 before everything else. It is the only step that removes edges without
requiring a single design decision, and it shrinks the surface that steps 2 and 3
have to reason about. Doing `Browser` first means designing interfaces against a
graph that still has `PageSnapshot` and `ExtractionResult` in the wrong places,
which will produce interfaces that are wider than they need to be.

---

## 8. Tooling handoff

`/tmp/gcanalyze/` (`go run . /path/to/engine`) prints, for any directory of Go
files in one package:

- pure roots, with out-degree and the symbols each one still needs;
- pure leaves, with in-degree;
- the movable closure (files whose every referencer is itself movable);
- hub symbols referenced by four or more distinct files;
- cyclic group pairs with the full symbol list in each direction.

The group map is a literal at the top of the group-graph section in `main.go`;
edit it when files move so the cycle report stays meaningful.

Re-run it after every step above. The measurement is what tells you whether an
inversion actually removed an edge or just moved it.
