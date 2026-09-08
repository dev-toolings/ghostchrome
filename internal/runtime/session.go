// Package runtime owns the op orchestration shared by every agent-facing
// surface: the JSONL loop (`ghostchrome agent`), the autonomous LLM loop
// (`ghostchrome ai`), and anything else that needs to drive a long-lived
// browser session op by op.
//
// It deliberately knows nothing about cobra, stdout, or output redaction.
// A Session takes a Config, dispatches an op by name, and RETURNS the
// result; encoding and transport are the caller's job.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/feedback"
	"github.com/go-rod/rod"
)

// Config carries everything the op layer used to read from cobra globals.
// The CLI still owns the flags; it fills a Config once and hands it over.
type Config struct {
	// SessionName returns the resolved named session, or "" when none. It is
	// a func because BrowserOpts may resolve the name lazily (the implicit
	// daemon picks a default on the first launch).
	SessionName func() string

	// BrowserOpts builds the launch/connect options. Called once, on the
	// first EnsurePage.
	BrowserOpts func() engine.BrowserOpts

	// ResolveOutputPath validates and absolutises ObserveOut. Nil means
	// "use the path as-is".
	ResolveOutputPath func(string) (string, error)

	// Stealth mirrors --stealth: applies the anti-detection patches and
	// arms the bot-challenge recovery path.
	Stealth bool

	// Observe mirrors --observe: starts the CDP observer sidecar.
	Observe bool

	// ObserveOut mirrors --observe-out: NDJSON sidecar for observer events.
	ObserveOut string

	// TimeoutSec mirrors --timeout, in seconds. Zero falls back to 30s.
	TimeoutSec int

	// Version is reported by the "init" op.
	Version string
}

func (c Config) sessionName() string {
	if c.SessionName == nil {
		return ""
	}
	return c.SessionName()
}

func (c Config) browserOpts() engine.BrowserOpts {
	if c.BrowserOpts == nil {
		return engine.BrowserOpts{Headless: true, TimeoutSec: c.TimeoutSec}
	}
	return c.BrowserOpts()
}

func (c Config) resolveOutputPath(p string) (string, error) {
	if c.ResolveOutputPath == nil {
		return p, nil
	}
	return c.ResolveOutputPath(p)
}

// Session holds long-lived state across ops: the browser, the current page,
// the last snapshot, and the observer sidecar. Refs handed out by one op stay
// valid for the next one.
type Session struct {
	cfg Config

	browser         *engine.Browser
	page            *rod.Page
	snapshot        *engine.PageSnapshot // last in-memory snapshot (auto-launch mode)
	rt              *engine.Runtime
	observer        *engine.Observer        // non-nil when Observe is active
	obsFile         *os.File                // non-nil when ObserveOut is set
	lastObservation *feedback.Observation   // observation from the previous op
	recoveryHooks   []feedback.RecoveryHook // pluggable recovery hooks

	// challengeRecovered records whether the last "navigate" op detected AND
	// cleared a bot challenge (DataDome/Cloudflare interstitial). "extract"
	// reads it to opt into the SSR fallback (includeSSR) on the recovery
	// path — the primary reason an automation client drives this JSONL loop
	// against a DataDome-protected, SSR-rendered (Next.js) site in the first
	// place. Always overwritten by "navigate" (never just set-if-true), so a
	// later clean navigate correctly resets it.
	//
	// One-shot: "extract" consumes it via consumeChallengeRecovered, which
	// resets it to false immediately after reading. Any op that navigates or
	// changes the current page (see resetsChallengeRecovered) also resets it
	// preemptively, so only the "extract" immediately following a recovered
	// "navigate" opts into SSR — not every extract until the next navigate.
	challengeRecovered bool
	dialogPolicy       *engine.DialogAutoPolicy

	// external is set by Bind: the browser and the page belong to a surface
	// that manages the Chrome lifecycle itself. Such a session must never
	// launch a browser and never close one.
	external bool
}

// New constructs a session ready to be driven op by op. The browser is opened
// lazily on the first EnsurePage (or first op that needs a page).
func New(cfg Config) *Session {
	return &Session{
		cfg:           cfg,
		recoveryHooks: feedback.DefaultRecoveryHooks(),
	}
}

// Page returns the current page, or nil before the first op that opens one.
func (s *Session) Page() *rod.Page { return s.page }

// Observer returns the CDP observer sidecar, or nil when none is attached.
func (s *Session) Observer() *engine.Observer { return s.observer }

// LastObservation returns the observation built by the previous RunOp call.
func (s *Session) LastObservation() *feedback.Observation { return s.lastObservation }

// Shutdown releases the browser, the observer and the sidecar file. It is a
// no-op on a bound session: the surface that owns the browser closes it.
func (s *Session) Shutdown() {
	if s.external {
		return
	}
	if s.rt != nil {
		s.rt.Close()
		s.rt = nil
		s.browser = nil
		s.observer = nil
		if s.obsFile != nil {
			_ = s.obsFile.Close()
			s.obsFile = nil
		}
		return
	}
	if s.observer != nil {
		_ = s.observer.Stop()
	}
	if s.obsFile != nil {
		_ = s.obsFile.Close()
	}
	if s.browser != nil {
		s.browser.Close()
	}
}

// EnsurePage opens the browser and the first page on demand, and is a no-op
// once a page exists.
func (s *Session) EnsurePage() (*engine.Browser, *rod.Page, error) {
	if s.page != nil {
		return s.browser, s.page, nil
	}
	if s.external {
		// Bound sessions never launch: the surface owns the lifecycle and
		// rebinds before every op, so a missing page means the surface has
		// torn Chrome down rather than that one should be started here.
		return nil, nil, errors.New("runtime: no page bound (the surface owns the browser lifecycle)")
	}
	opts := s.cfg.browserOpts()
	b, err := engine.NewBrowserWith(opts)
	if err != nil {
		return nil, nil, fmt.Errorf("browser: %w", err)
	}
	page, err := b.Page()
	if err != nil {
		b.Close()
		return nil, nil, fmt.Errorf("page: %w", err)
	}
	if s.cfg.Stealth {
		if err := engine.ApplyStealth(page); err != nil {
			fmt.Fprintf(os.Stderr, "warning: stealth not fully applied: %v\n", err)
		}
	}
	s.browser = b
	s.page = page
	s.rt = engine.NewRuntime(b)
	if s.dialogPolicy == nil {
		s.dialogPolicy = &engine.DialogAutoPolicy{Accept: true}
	}
	engine.StartDialogAutoHandler(page, s.dialogPolicy)
	if name := s.cfg.sessionName(); name != "" {
		engine.TouchSessionLease(name)
	}
	if !s.cfg.Stealth {
		if hub := s.rt.AttachEvents(page); hub != nil {
			s.observer = hub.Observer()
		}
	}

	// Start observer sidecar if Observe is active.
	if s.cfg.Observe {
		if s.cfg.Stealth {
			fmt.Fprintln(os.Stderr, "ghostchrome: --observe enables the Runtime CDP domain, weakening --stealth")
		}
		if s.observer == nil {
			obs := engine.NewObserver(page, engine.ObserverOpts{})
			if err := obs.Start(context.Background()); err != nil {
				fmt.Fprintf(os.Stderr, "agent: observer start failed: %v\n", err)
			} else {
				s.observer = obs
				go func() {
					for range obs.Events() {
					}
				}()
			}
		}
		// Open file if ObserveOut is set.
		if s.cfg.ObserveOut != "" {
			safe, err := s.cfg.resolveOutputPath(s.cfg.ObserveOut)
			if err != nil {
				fmt.Fprintf(os.Stderr, "agent: observe-out: %v\n", err)
			} else {
				f, err := os.OpenFile(safe, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
				if err != nil {
					fmt.Fprintf(os.Stderr, "agent: observe-out open: %v\n", err)
				} else {
					s.obsFile = f
				}
			}
		}
	}

	return b, page, nil
}

// handler is one entry of the dispatch table. Handlers may return any
// JSON-encodable value; nil means "ok with no payload".
type handler func(*Session, json.RawMessage) (any, error)

// handlers is the single source of truth for the JSONL op surface: Dispatch
// routes through it and Ops enumerates it, so the two can never drift.
var handlers = map[string]handler{
	"init":     (*Session).opInit,
	"navigate": (*Session).opNavigate,
	"back":     func(s *Session, _ json.RawMessage) (any, error) { return s.opHistory(-1) },
	"forward":  func(s *Session, _ json.RawMessage) (any, error) { return s.opHistory(+1) },
	"reload":   func(s *Session, _ json.RawMessage) (any, error) { return s.opReload() },
	"extract":  (*Session).opExtract,
	"click":    func(s *Session, raw json.RawMessage) (any, error) { return s.opRef(raw, "click") },
	"dblclick": func(s *Session, raw json.RawMessage) (any, error) { return s.opRef(raw, "dblclick") },
	"check":    func(s *Session, raw json.RawMessage) (any, error) { return s.opCheck(raw, true) },
	"uncheck":  func(s *Session, raw json.RawMessage) (any, error) { return s.opCheck(raw, false) },
	"type":     (*Session).opType,
	"press":    (*Session).opPress,
	"hover":    func(s *Session, raw json.RawMessage) (any, error) { return s.opRef(raw, "hover") },
	"select":   (*Session).opSelect,
	"fill":     (*Session).opFill,

	"scroll_by":  (*Session).opScrollBy,
	"scroll_to":  (*Session).opScrollTo,
	"eval":       (*Session).opEval,
	"screenshot": (*Session).opScreenshot,
	"wait":       (*Session).opWait,
	"errors":     func(s *Session, _ json.RawMessage) (any, error) { return s.opErrors() },
	"url":        func(s *Session, _ json.RawMessage) (any, error) { return s.opURL() },
	"dialog":     (*Session).opDialog,
	"tabs":       (*Session).opTabs,
	"close":      func(_ *Session, _ json.RawMessage) (any, error) { return nil, nil },
}

// Ops returns the sorted op names this package handles. It is derived from the
// dispatch table, so every surface that needs to enumerate the JSONL protocol
// (contract generation, parity tests, SDK coverage) reads the truth instead of
// a hand-maintained copy.
func Ops() []string {
	names := make([]string, 0, len(handlers))
	for name := range handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Handles reports whether op is part of the JSONL surface.
func Handles(op string) bool {
	_, ok := handlers[op]
	return ok
}

// Dispatch routes the op to its handler.
func (s *Session) Dispatch(op string, args json.RawMessage) (any, error) {
	if resetsChallengeRecovered(op) {
		s.challengeRecovered = false
	}
	h, ok := handlers[op]
	if !ok {
		return nil, fmt.Errorf("unknown op %q", op)
	}
	return h(s, args)
}

// RunOp executes one op and returns the result plus the per-op observer events
// and the freshly built Observation, so callers do not have to duplicate the
// snapshot bookkeeping around every dispatch.
func (s *Session) RunOp(op string, args json.RawMessage) (any, *feedback.Observation, []engine.ObserverEvent, error) {
	tStart := time.Now().UnixMilli()
	snapshotBefore := s.currentSnapshotIfAvailable()
	result, err := s.Dispatch(op, args)
	var events []engine.ObserverEvent
	if s.observer != nil {
		events = s.observer.Drain(tStart)
	}
	var obs *feedback.Observation
	if s.page != nil {
		snapshotAfter := s.currentSnapshotIfAvailable()
		scanCaptcha := s.cfg.Observe || op == "navigate" || op == "reload"
		built := feedback.BuildObservationOpts(s.page, snapshotBefore, snapshotAfter, events, scanCaptcha)
		obs = &built
		s.lastObservation = obs
	}
	return result, obs, events, err
}

// MirrorEvents appends observer events to the ObserveOut sidecar when one is
// open. No-op otherwise.
func (s *Session) MirrorEvents(events []engine.ObserverEvent) {
	if s.obsFile == nil || len(events) == 0 {
		return
	}
	enc := json.NewEncoder(s.obsFile)
	for _, evt := range events {
		_ = enc.Encode(evt)
	}
}

// resetsChallengeRecovered reports whether the given JSONL op navigates or
// otherwise changes the current page. Such ops must reset challengeRecovered
// preemptively: the one-shot SSR opt-in is only meant for the "extract" call
// immediately following a recovered "navigate", not for an unrelated page
// reached via an intervening click/press/back/forward/reload/type/select.
func resetsChallengeRecovered(op string) bool {
	switch op {
	case "back", "forward", "reload", "click", "dblclick", "type", "press", "select":
		return true
	default:
		return false
	}
}

// consumeChallengeRecovered returns the current SSR opt-in flag and resets it
// to false. One-shot: only the first "extract" after a recovered "navigate"
// sees it as true.
func (s *Session) consumeChallengeRecovered() bool {
	v := s.challengeRecovered
	s.challengeRecovered = false
	return v
}

func (s *Session) adoptPage(b *engine.Browser, page *rod.Page) error {
	if err := b.SetCurrentPage(page); err != nil {
		return err
	}
	s.page = page
	s.snapshot = b.Snapshot(page)
	if s.dialogPolicy == nil {
		s.dialogPolicy = &engine.DialogAutoPolicy{Accept: true}
	}
	engine.StartDialogAutoHandler(page, s.dialogPolicy)
	if s.rt == nil {
		s.rt = engine.NewRuntime(b)
	}
	if !s.cfg.Stealth {
		if hub := s.rt.AttachEvents(page); hub != nil {
			s.observer = hub.Observer()
		}
	}
	return nil
}

func snapshotModeFromArgs(raw json.RawMessage) engine.SnapshotMode {
	var a struct {
		Snapshot string `json:"snapshot"`
	}
	_ = unmarshalArgs(raw, &a)
	mode, err := engine.ParseSnapshotMode(a.Snapshot)
	if err != nil {
		return engine.SnapshotModeDiff
	}
	return mode
}

// mutationResult honours none/diff/full. snapshot=full returns the skeleton
// ExtractionResult (same shape as extract) so agents can keep refs without a
// second extract call. none skips AX work after the mutation.
func (s *Session) mutationResult(b *engine.Browser, page *rod.Page, prev *engine.PageSnapshot, mode engine.SnapshotMode) (any, error) {
	failure := func(err error) (any, error) {
		// Classify explicitly: a completed click must not become a retryable
		// timeout and execute twice merely because observation failed.
		return nil, &engine.OpError{Code: engine.ErrCodeUnknown, Retryable: false, Err: fmt.Errorf("action completed, but post-action snapshot failed (do not repeat the action; extract again): %w", err)}
	}
	switch mode {
	case engine.SnapshotModeNone:
		if b != nil {
			_ = b.InvalidateCachedExtract(page)
		}
		return engine.SnapshotDiff{Unchanged: true}, nil
	case engine.SnapshotModeFull:
		if b != nil {
			_ = b.InvalidateCachedExtract(page)
		}
		if err := engine.WaitForImminentDOM(page, 0); err != nil {
			return failure(err)
		}
		result, err := engine.Extract(page, engine.LevelSkeleton, "", false)
		if err != nil {
			return failure(err)
		}
		if b != nil {
			_ = b.SaveSnapshot(page, result)
			if snap, serr := engine.BuildSnapshot(page, result); serr == nil {
				s.snapshot = snap
			}
		}
		return result, nil
	default:
		diff, result, err := engine.CaptureMutation(b, page, prev)
		if err != nil {
			return failure(err)
		}
		s.rememberMutationRefs(b, page, result)
		return diff, nil
	}
}

// rememberMutationRefs advances the ref table to the state CaptureMutation just
// observed.
//
// Browser.Snapshot is only populated in connected mode (a named session with an
// on-disk state file). With an embedded Chrome it returns nil, so the freshly
// captured skeleton has to be built here — otherwise the ref table stays pinned
// to the last "extract" and every following mutation re-reports the same stale
// difference between the content-level extract and the skeleton.
func (s *Session) rememberMutationRefs(b *engine.Browser, page *rod.Page, result *engine.ExtractionResult) {
	if b != nil {
		if snap := b.Snapshot(page); snap != nil {
			s.snapshot = snap
			return
		}
	}
	if result == nil || page == nil {
		return
	}
	if snap, err := engine.BuildSnapshot(page, result); err == nil {
		s.snapshot = snap
	}
}

// withRecovery calls fn with the current snapshot; if it fails, it runs the
// recovery hook chain. If a hook signals retry=true the op is retried once.
//
// RecoverStaleRef (built-in) explicitly refuses to silently remap refs — it
// returns retry=false with an informative error requiring the agent to run an
// explicit re-extract op. This is intentional (see Vague D notes).
func (s *Session) withRecovery(b *engine.Browser, page *rod.Page, opName string, fn func(*engine.PageSnapshot) error) error {
	snap := s.currentSnapshot(b, page)
	err := fn(snap)
	if err == nil {
		if b != nil && page != nil {
			_ = b.InvalidateCachedExtract(page)
		}
		return nil
	}
	if len(s.recoveryHooks) == 0 {
		return err
	}
	ctx := feedback.RecoveryContext{Page: page, Err: err, OpName: opName}
	retry, hookErr := feedback.RecoveryChain(ctx, s.recoveryHooks)
	if hookErr != nil {
		return hookErr
	}
	if !retry {
		return err
	}
	// One retry after recovery.
	if err := fn(s.currentSnapshot(b, page)); err != nil {
		return err
	}
	if b != nil && page != nil {
		_ = b.InvalidateCachedExtract(page)
	}
	return nil
}

// withRefRetry is a legacy alias kept for call sites that pre-date withRecovery.
// New code should call withRecovery directly.
func (s *Session) withRefRetry(b *engine.Browser, page *rod.Page, fn func(*engine.PageSnapshot) error) error {
	return s.withRecovery(b, page, "ref", fn)
}

// currentSnapshot returns the most relevant snapshot: the connected-mode
// session snapshot if available, else the in-memory one tracked by the
// agent session.
func (s *Session) currentSnapshot(b *engine.Browser, page *rod.Page) *engine.PageSnapshot {
	if snap := b.Snapshot(page); snap != nil {
		return snap
	}
	return s.snapshot
}

// currentSnapshotIfAvailable returns a snapshot without requiring a browser/page
// pair — used before the page is initialised (pre-op snapshot for diffs).
func (s *Session) currentSnapshotIfAvailable() *engine.PageSnapshot {
	if s.browser != nil && s.page != nil {
		if snap := s.browser.Snapshot(s.page); snap != nil {
			return snap
		}
	}
	return s.snapshot
}

// unmarshalArgs handles the common case of optional / empty args.
func unmarshalArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}
