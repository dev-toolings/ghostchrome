# Cross-origin widget interaction investigation

Validated on 2026-10-29 against https://siv-auto.fr/ and controlled Go browser
fixtures. This is evidence from one environment, not a Cloudflare acceptance
promise. The installed MCP executable was not replaced by this change.

## Verified defects and fixes

- Scoped extraction skipped all iframe traversal. The widget scope therefore
  contained an iframe title but no actionable refs. Frame discovery now starts
  inside the selected subtree before deciding whether an empty scope requires
  fallback to the whole page.
- CSS iframe discovery missed closed shadow roots. CDP DOM traversal now walks
  ordinary children and shadow roots, stopping at frame owners. Child documents
  are inspected separately, with the existing three-level bound.
- Rod's `Element.Frame()` keeps the parent session. That works for same-process
  frames but not OOPIFs. Remote frame targets now use their own cached Rod/CDP
  sessions, without applying top-level viewport emulation to the child target
  or disabling site isolation.
- Ref lookup and semantic retries must stay in the original frame. Missing
  frames now return stale-ref errors instead of looking in the parent page.
  Duplicate-name ordinals are counted per frame, and same-process semantic AX
  queries explicitly select the child frame.
- The live checkbox was an `opacity:0` native input with a nonempty 182 by 24
  pixel box inside a second closed shadow root. Actionability incorrectly
  rejected transparency and then misidentified the closed shadow host as an
  overlay. Hit testing now follows the target's known shadow roots while
  retaining geometry, disabled-state and occlusion checks. Mouse and keyboard
  dispatch remain ordinary browser input; no validation value is injected.
- Automatic recovery hid the original click error behind a Cloudflare timeout.
  A failed recovery now preserves both errors, including their error identities.

These paths are shared by MCP, JSONL and CLI. No operation names, arguments, SDK
result shapes, browser launch defaults or blocker patterns changed.

## Live evidence

The registered MCP installation was in MCP mode with `--stealth`. Its running
Chrome command line had `--headless=new`, an ephemeral profile, and no switches
that disable site isolation. The executable came from Rod's local browser cache;
CDP reported `Chrome/128.0.6568.0`. No system Chrome binary was found on PATH or
at the launcher's normal system locations. Configuration alone must not be
reported as evidence of a modern system browser or a headed session.

The initial scoped snapshot reproduced the iframe-only result. Turnstile's API
script and challenge document returned HTTP 200; the observed blocked request
was Google advertising JavaScript. The curated blocker has no matching
Cloudflare patterns, now guarded by a regression test.

A temporary, opt-in Go harness exercised the modified production MCP handler
and runtime with one isolated persistent profile, sequentially:

| Mode | Blocker | Interaction | Outcome |
|---|---|---|---|
| Headless | Enabled | Checkbox ref discovered; click completed | No validation token within the observed 15 seconds; no form submission |
| Headed under Xvfb | Enabled | Checkbox ref discovered; click completed | Token present; widget displayed Success; authorized form submission reached `/infos.php` and displayed the supplied plate and KIA |

The successful submission was sent only to siv-auto.fr, after checking the form
origin and `/index.php` action. No token or CSRF value was recorded or replaced.
The initial investigation did not retrieve full vehicle specifications; the
follow-up measurement below does. Xvfb is a
virtual display; this does not establish behavior on a physical desktop. The
comparison reused a persistent profile and was not a randomized experiment, so
it does not prove headless mode alone caused the earlier non-acceptance.

Local screenshots are `.local/siv-headless.png` and `.local/siv-headed.png`.
The temporary harness source is retained only in ignored `.local/`; it is not
part of CI and is not installed or registered as a replacement MCP server.
Task-owned browsers were closed after each run.
The isolated profile is retained at
`~/.ghostchrome/profiles/siv-investigation-20261029` for reproducibility.

## External implementation references

- [Reader block detector](https://github.com/vakra-dev/reader/blob/main/src/utils/block-detector.ts):
  distinguishes transport status from blocked content. This informed the
  investigation's separate token, submission and result checks; Reader was not
  added as a dependency or copied into the Go runtime.
- [Reader launch settings](https://github.com/vakra-dev/reader/blob/main/src/browser/shared.ts):
  inspected alongside its browser pool. Its site-isolation-disabling flags were
  deliberately not adopted. Such flags would conceal the OOPIF defect.
- [Playwright Chromium frame sessions](https://github.com/microsoft/playwright/blob/main/packages/playwright-core/src/server/chromium/crPage.ts):
  frame-to-session routing and explicit OOPIF attachment informed the native Go
  implementation. Chromium frame IDs identify remote frame targets.
- [Playwright actionability](https://playwright.dev/docs/actionability#visible):
  transparent elements with geometry can still receive input. Geometry and hit
  testing, rather than opacity alone, govern the corrected check.
- [Rod v0.116.2 frame implementation](https://github.com/go-rod/rod/blob/v0.116.2/element.go)
  and [target attachment](https://github.com/go-rod/rod/blob/v0.116.2/browser.go):
  verified against the dependency source installed for this repository.
- [CDP Accessibility](https://chromedevtools.github.io/devtools-protocol/tot/Accessibility/):
  `getFullAXTree.frameId` addresses the document whose AX tree is requested.

## Regression coverage

`TestFrameExtractionAndInput` uses production launcher flags and local HTTP
fixtures with different sites. It checks whole-page and scoped snapshots,
closed roots around both the frame and the native input, distinct OOPIF
sessions, nested frames, trusted mouse and keyboard events, semantic recovery
after replacement, occlusion rejection, and detached-frame failure.

Hermetic tests cover frame-local semantic ordinals, preservation of original
errors when recovery fails, and the curated blocker's compatibility with
Turnstile resource URLs. Existing iframe and scope tests remain applicable.
Live provider acceptance is deliberately excluded from hermetic pass criteria.

Completed validation: `go test -short ./...`, the targeted engine browser tests
for frames, scopes, semantic refs and actionability, and
`go vet ./internal/core/engine ./internal/core/antibot ./internal/runtime`.
Diff whitespace, added punctuation and documented paths were checked; the
`AGENTS.md` symlink is preserved. No standalone production build, installation
replacement, release or push was performed.


## Follow-up: warm latency and HTTP retrieval

Measured on 2026-10-29 through an opt-in Go MCP/runtime harness, with headed
Chrome under Xvfb and the same isolated persistent profile. Three authorized
searches used only the supplied test plate and submitted only to siv-auto.fr.
The browser stayed open between the first two searches. The third search was
performed after relaunching Chrome, following an initial navigation to warm it.

| Stage (milliseconds) | Warm search 1 | Warm search 2 | Warm search 3 |
|---|---|---|---|
| Homepage navigation, load wait | 362 | 205 | 301 |
| Widget discovery, click, token wait | 1982 | 2055 | 2239 |
| Submission until `/infos.php` URL observed | 240 | 311 | 243 |
| Warm total until `/infos.php` | 2585 | 2572 | 2784 |

Startup plus initial navigation measured 952 and 983 ms in the two harness runs.
These stages exclude screenshots and shutdown. The token poll interval was
100 ms and the result-URL poll interval was 50 ms. The submission used the native
form's `requestSubmit` after checking its same-origin `/index.php` destination;
it did not inject a validation token. URL observation is not a full render-time
measurement. Three successes are a small sample, not a percentile benchmark.

The page links to `result.php`. A standard Go HTTP client, without Chrome/CDP,
successfully fetched both pages after receiving only siv-auto.fr cookies from
the validated browser into a memory-only cookie jar. Redirects were restricted
to HTTPS on that exact host. No cookie, CSRF or Turnstile values were logged.

| HTTP GET | With validated session | Without session |
|---|---|---|
| `/infos.php` | 61 ms, 200, vehicle present | 199-201 ms, redirected to `/index.php`, no vehicle |
| `/result.php` | 66 ms, 200, complete vehicle characteristics | 118 ms, redirected to `/index.php`, no vehicle |

The HTML result identifies KIA STONIC, phase 2, `1.0i TURBO EH 120 FAP`, five-door
estate body, engine G3LF and first registration 17/09/2021. Other returned fields
include a VIN and colour; identifiers are intentionally not reproduced here.
The result is server-rendered HTML, not a demonstrated JSON API.

This proves HTTP retrieval of an already validated search result. It does not
prove a new plate lookup works over HTTP with cookies alone. For iautos-agent,
the measured viable route is a persistent browser for normal validation and
submission, followed by HTTP retrieval and parsing of `result.php`. The measured
warm lookup still costs about 2.6-2.8 seconds before the final HTTP read. A
subsecond uncached lookup has not been demonstrated. Session state holds the
current search, so concurrent lookups must not share and overwrite one session.

The temporary harness is retained at ignored `.local/siv_latency_manual_test.go`.
It is not a new public operation or an iautos-agent integration. Both harness
runs passed, their owned browsers closed, and repository diff checks passed.
