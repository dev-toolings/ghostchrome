package engine

import (
	"context"
	"sync"
	"time"

	"github.com/go-rod/rod"
)

// Ownership says what Close/Release is allowed to destroy.
type Ownership int

const (
	// OwnershipEphemeral: ghostchrome launched this Chrome and may Browser.close it.
	OwnershipEphemeral Ownership = iota
	// OwnershipManaged: a ghostchrome serve/daemon owns the process. Detach only.
	OwnershipManaged
	// OwnershipAttached: an external Chrome. Never send Browser.close.
	OwnershipAttached
	// OwnershipProvider: a cloud/provider callback owns teardown.
	OwnershipProvider
)

func (o Ownership) String() string {
	switch o {
	case OwnershipEphemeral:
		return "ephemeral"
	case OwnershipManaged:
		return "managed"
	case OwnershipAttached:
		return "attached"
	case OwnershipProvider:
		return "provider"
	default:
		return "unknown"
	}
}

// DefaultActionTimeout is the per-operation budget used when the caller
// does not set a tighter deadline. Kept below typical CLI/SDK read
// timeouts so a missing element returns a typed error instead of EAGAIN.
const DefaultActionTimeout = 8 * time.Second

// Runtime is the session-scoped execution handle. CLI, JSONL and MCP
// should drive pages through it so ownership, budgets and ref maps stay
// consistent.
type Runtime struct {
	Browser    *Browser
	Ownership  Ownership
	Generation uint64

	mu  sync.Mutex
	hub *EventHub
}

// NewRuntime wraps an existing Browser with explicit ownership inferred
// from how it was opened. Prefer this over reading Browser.connected.
func NewRuntime(b *Browser) *Runtime {
	if b == nil {
		return nil
	}
	return &Runtime{Browser: b, Ownership: b.ownership, Generation: 1}
}

// PageSession binds a target/page to this runtime.
func (r *Runtime) PageSession(page *rod.Page) *PageSession {
	if r == nil || page == nil {
		return nil
	}
	return &PageSession{rt: r, page: page, timeout: actionTimeout(r.Browser)}
}

// AttachEvents starts the shared EventHub for page. Call this from
// long-lived JSONL/MCP sessions; skip it under stealth so we do not
// send Runtime.enable.
func (r *Runtime) AttachEvents(page *rod.Page) *EventHub {
	r.ensureHub(page)
	return r.EventHub()
}

func (r *Runtime) ensureHub(page *rod.Page) {
	if r == nil || page == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hub != nil && r.hub.page == page {
		return
	}
	if r.hub != nil {
		r.hub.Stop()
		r.hub = nil
	}
	hub, err := StartEventHub(page)
	if err != nil {
		return
	}
	r.hub = hub
}

// EventHub returns the shared CDP subscriber, if started.
func (r *Runtime) EventHub() *EventHub {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hub
}

// Close stops the event hub then applies Browser ownership rules.
func (r *Runtime) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	hub := r.hub
	r.hub = nil
	r.mu.Unlock()
	if hub != nil {
		hub.Stop()
	}
	if r.Browser != nil {
		r.Browser.Close()
	}
}

func actionTimeout(b *Browser) time.Duration {
	if b != nil && b.timeout > 0 && b.timeout < DefaultActionTimeout {
		return b.timeout
	}
	return DefaultActionTimeout
}

// OpContext returns a per-operation context that cannot outlive parent.
func OpContext(parent context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if budget <= 0 {
		budget = DefaultActionTimeout
	}
	return context.WithTimeout(parent, budget)
}

// PageWithBudget clones page with a per-call Rod timeout. The returned
// page must not be stored as the long-lived session page.
func PageWithBudget(page *rod.Page, budget time.Duration) *rod.Page {
	if page == nil {
		return nil
	}
	if budget <= 0 {
		budget = DefaultActionTimeout
	}
	return page.Timeout(budget)
}

func navTimeout(page *rod.Page) time.Duration {
	if page == nil {
		return NavWaitTimeout
	}
	if dl, ok := page.GetContext().Deadline(); ok {
		remain := time.Until(dl)
		if remain > 0 && remain < NavWaitTimeout {
			return remain
		}
	}
	return NavWaitTimeout
}
