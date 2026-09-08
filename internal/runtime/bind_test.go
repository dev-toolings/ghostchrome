package runtime

import (
	"encoding/json"
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
)

// A bound session must never launch Chrome on its own: the surface owns the
// lifecycle and rebinds before every op, so an unbound op has to fail rather
// than spawn a second browser behind the surface's back.
func TestBoundSessionNeverLaunchesABrowser(t *testing.T) {
	s := New(Config{})
	s.Bind(Binding{})

	if _, _, err := s.EnsurePage(); err == nil {
		s.Shutdown()
		t.Fatal("expected EnsurePage to refuse to launch on a bound session")
	}
	if _, err := s.Dispatch("navigate", json.RawMessage(`{"url":"https://example.com"}`)); err == nil {
		s.Shutdown()
		t.Fatal("expected navigate to fail without a bound page")
	}
	if s.browser != nil {
		s.Shutdown()
		t.Fatal("a bound session opened a browser")
	}
}

// Shutdown must not close a browser the session does not own.
func TestBoundSessionShutdownIsANoOp(t *testing.T) {
	s := New(Config{})
	s.Bind(Binding{})
	s.Shutdown() // would panic or close nothing; must simply return
	if !s.external {
		t.Fatal("Bind did not mark the session external")
	}
}

// Bind must not drop the ref table: refs handed out by one tool call stay
// valid for the next one, which is what a long-lived surface session is for.
func TestBindPreservesTheRefTable(t *testing.T) {
	s := New(Config{})
	snap := &engine.PageSnapshot{}
	s.SetRefs(snap)
	s.Bind(Binding{})
	if s.Refs() != snap {
		t.Fatal("Bind dropped the ref table")
	}
	s.SetRefs(nil)
	if s.Refs() != nil {
		t.Fatal("SetRefs(nil) did not clear the ref table")
	}
}

// A policy configured by the surface must be the object the session installs
// on adopted targets, otherwise a click-popup silently reverts to accept-all.
func TestBindAdoptsTheSurfaceDialogPolicy(t *testing.T) {
	s := New(Config{})
	policy := &engine.DialogAutoPolicy{Accept: false}
	s.Bind(Binding{DialogPolicy: policy})
	if s.dialogPolicy != policy {
		t.Fatal("Bind did not adopt the surface dialog policy")
	}
	// A later Bind without a policy must not clear the one already held.
	s.Bind(Binding{})
	if s.dialogPolicy != policy {
		t.Fatal("Bind cleared the dialog policy")
	}
}
