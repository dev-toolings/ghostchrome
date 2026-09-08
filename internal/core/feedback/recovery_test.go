package feedback

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/coretest"
)

// TestRecoverStaleRefRefusesRetry verifies that RecoverStaleRef never signals
// retry=true — it must always surface the error so the agent re-extracts.
func TestRecoverStaleRefRefusesRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := coretest.NewIsolatedPage(t)

	ctx := RecoveryContext{
		Page:   page,
		Err:    fmt.Errorf("some error: %w", engine.ErrStaleRef),
		OpName: "click",
	}
	retry, err := RecoverStaleRef(ctx)
	if retry {
		t.Fatal("RecoverStaleRef must never return retry=true")
	}
	if err == nil {
		t.Fatal("RecoverStaleRef must return a non-nil error wrapping ErrStaleRef")
	}
	if !errors.Is(err, engine.ErrStaleRef) {
		t.Fatalf("expected error to wrap ErrStaleRef, got: %v", err)
	}
}

// TestRecoverStaleRefSkipsNonStaleErrors confirms the hook is a no-op for
// errors that are not ErrStaleRef.
func TestRecoverStaleRefSkipsNonStaleErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := coretest.NewIsolatedPage(t)

	ctx := RecoveryContext{
		Page:   page,
		Err:    errors.New("some other error"),
		OpName: "click",
	}
	retry, err := RecoverStaleRef(ctx)
	if retry {
		t.Fatal("expected retry=false for non-stale error")
	}
	if err != nil {
		t.Fatalf("expected nil err for non-stale error, got: %v", err)
	}
}

// TestRecoverNetworkSettleSkipsNonTimeoutErrors verifies the network-settle
// hook only fires on timeout-flavoured errors.
func TestRecoverNetworkSettleSkipsNonTimeoutErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := coretest.NewIsolatedPage(t)

	ctx := RecoveryContext{
		Page:   page,
		Err:    errors.New("some unrelated error"),
		OpName: "navigate",
	}
	retry, err := RecoverNetworkSettle(ctx)
	if retry {
		t.Fatal("expected retry=false for non-timeout error")
	}
	if err != nil {
		t.Fatalf("expected nil err, got: %v", err)
	}
}

// TestRecoverNetworkSettleTriggersOnTimeoutError verifies the hook fires on
// timeout-like errors and (on a live page) signals retry.
func TestRecoverNetworkSettleTriggersOnTimeoutError(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<!doctype html><html><body><p>ok</p></body></html>")
	}))
	defer server.Close()

	_, page := coretest.NewIsolatedPage(t)
	if _, err := engine.Navigate(page, server.URL, "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	ctx := RecoveryContext{
		Page:   page,
		Err:    errors.New("operation timed out"),
		OpName: "navigate",
	}
	retry, err := RecoverNetworkSettle(ctx)
	if err != nil {
		t.Fatalf("expected nil err, got: %v", err)
	}
	if !retry {
		t.Fatal("expected retry=true for timeout error on idle page")
	}
}

// TestRecoveryChainFirstHookWins verifies that the chain stops at the first
// hook that returns retry=true.
func TestRecoveryChainFirstHookWins(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	calls := 0
	h1 := func(ctx RecoveryContext) (bool, error) {
		calls++
		return true, nil // signals recovery
	}
	h2 := func(ctx RecoveryContext) (bool, error) {
		calls++
		return false, nil
	}

	_, page := coretest.NewIsolatedPage(t)
	ctx := RecoveryContext{Page: page, Err: errors.New("err"), OpName: "op"}
	retry, err := RecoveryChain(ctx, []RecoveryHook{h1, h2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !retry {
		t.Fatal("expected retry=true")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 hook call, got %d", calls)
	}
}

// TestRecoveryChainHookError propagates hook errors.
func TestRecoveryChainHookError(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	hookErr := errors.New("hook failed")
	h1 := func(ctx RecoveryContext) (bool, error) {
		return false, hookErr
	}

	_, page := coretest.NewIsolatedPage(t)
	ctx := RecoveryContext{Page: page, Err: errors.New("err"), OpName: "op"}
	retry, err := RecoveryChain(ctx, []RecoveryHook{h1})
	if retry {
		t.Fatal("expected retry=false on hook error")
	}
	if !errors.Is(err, hookErr) {
		t.Fatalf("expected hookErr, got %v", err)
	}
}

// TestRecoveryChainAllSkip verifies that if all hooks skip, chain returns
// retry=false, nil.
func TestRecoveryChainAllSkip(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	h1 := func(ctx RecoveryContext) (bool, error) { return false, nil }
	h2 := func(ctx RecoveryContext) (bool, error) { return false, nil }

	_, page := coretest.NewIsolatedPage(t)
	ctx := RecoveryContext{Page: page, Err: errors.New("err"), OpName: "op"}
	retry, err := RecoveryChain(ctx, []RecoveryHook{h1, h2})
	if retry {
		t.Fatal("expected retry=false")
	}
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
}
