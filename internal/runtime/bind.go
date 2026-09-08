package runtime

import (
	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
)

// Binding is the externally owned browser state a surface hands to the session
// before running an op.
//
// The JSONL loop lets the Session open and close Chrome itself. The MCP server
// cannot: it holds one browser across tool calls behind its own liveness probe,
// target recovery, idle reaper and per-request page cloning. Bind lets it keep
// all of that and still run every op through this package, so ref resolution,
// recovery and mutation diffing exist in one place instead of two.
//
// Every field stays owned by the caller. A bound session never launches and
// never closes a browser.
type Binding struct {
	// Browser and Page are the handles the next op runs against. Page may be a
	// short-lived, context-scoped clone: the session re-reads it on every Bind,
	// so a cancelled clone is never reused by a later op.
	Browser *engine.Browser
	Page    *rod.Page

	// Runtime is the engine runtime holding the per-page sessions. Sharing the
	// surface's instance (rather than building a second one) keeps page-session
	// state single.
	Runtime *engine.Runtime

	// DialogPolicy is the live policy object the surface installed on the page.
	// Ops that adopt a new target (click popup, tab switch) re-install this same
	// object, so a policy configured through the surface survives the adoption.
	DialogPolicy *engine.DialogAutoPolicy
}

// Bind attaches externally owned browser state. It does not touch the ref
// table: refs handed out by one op stay valid for the next, which is the whole
// point of a long-lived surface session.
func (s *Session) Bind(b Binding) {
	s.external = true
	s.browser = b.Browser
	s.page = b.Page
	s.rt = b.Runtime
	if b.DialogPolicy != nil {
		s.dialogPolicy = b.DialogPolicy
	}
}

// Refs returns the in-memory ref table (@1, @2, ...) the session currently
// holds. It is the fallback used when the browser holds no snapshot for the
// current page.
func (s *Session) Refs() *engine.PageSnapshot { return s.snapshot }

// SetRefs replaces the in-memory ref table. Surfaces call it with nil after
// tearing the browser down, so ref-based ops fail closed and tell the agent to
// re-extract instead of clicking into a dead target.
func (s *Session) SetRefs(snap *engine.PageSnapshot) { s.snapshot = snap }

// RememberExtraction records an extraction result as the session ref table and
// persists it on the browser. Composite surface ops that extract outside the
// dispatch table (the MCP "snapshot" tool bundles navigate+extract+errors) call
// it so their refs resolve exactly like refs from the "extract" op.
func (s *Session) RememberExtraction(result *engine.ExtractionResult) error {
	if result == nil || s.page == nil {
		return nil
	}
	snap, err := engine.BuildSnapshot(s.page, result)
	if err != nil {
		return err
	}
	s.snapshot = snap
	if s.browser != nil {
		_ = s.browser.SaveSnapshot(s.page, result)
	}
	return nil
}

// Mutation observes the page after an action performed outside the dispatch
// table (the MCP-only swipe and drag tools, and upload) and returns exactly
// what the mutation ops return for that mode: a compact SnapshotDiff, a
// skeleton ExtractionResult, or the unchanged marker.
//
// prev is the ref table captured before the action; Refs gives it.
func (s *Session) Mutation(prev *engine.PageSnapshot, mode engine.SnapshotMode) (any, error) {
	return s.mutationResult(s.browser, s.page, prev, mode)
}

// ResolveRef resolves an @ref against the session ref table, with the same
// recovery chain the mutation ops get. Surfaces that need the raw element
// (MCP's upload tool hands it to SetFiles) call this instead of resolving refs
// themselves.
func (s *Session) ResolveRef(ref string) (*rod.Element, error) {
	b, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	var el *rod.Element
	err = s.withRecovery(b, page, "resolve", func(snap *engine.PageSnapshot) error {
		resolved, rerr := engine.ResolveRef(page, ref, snap)
		el = resolved
		return rerr
	})
	if err != nil {
		return nil, err
	}
	return el, nil
}

// Drag performs a mouse drag-and-drop between two refs, resolving both through
// the session ref table with the usual recovery. There is no JSONL "drag" op —
// this is the shared implementation behind the MCP-only drag tool, kept here so
// the surface does not resolve refs on its own.
func (s *Session) Drag(fromRef, toRef string, steps int) error {
	b, page, err := s.EnsurePage()
	if err != nil {
		return err
	}
	return s.withRecovery(b, page, "drag", func(snap *engine.PageSnapshot) error {
		return engine.DragDrop(page, fromRef, toRef, snap, steps)
	})
}
