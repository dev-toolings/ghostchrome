package cli

import (
	"sync"
	"testing"
)

// The bug this guards: `exitErr` calls `os.Exit`, which skips every deferred
// function. A command that opened a page on an attached Chrome and then failed
// left that page open — one leaked renderer per failure. The cleanup stack is
// the only thing that runs on the exit path, so it has to be correct.

func resetCleanups(t *testing.T) {
	t.Helper()
	cleanupMu.Lock()
	cleanups = nil
	cleanupMu.Unlock()
}

func TestRunCleanupsRunsInReverseOrder(t *testing.T) {
	resetCleanups(t)

	var order []string
	registerCleanup(func() { order = append(order, "first") })
	registerCleanup(func() { order = append(order, "second") })

	runCleanups()

	// A page is registered after the browser that owns it; releasing in
	// registration order would close the browser out from under the page.
	want := []string{"second", "first"}
	if len(order) != len(want) {
		t.Fatalf("ran %d cleanups, want %d (%v)", len(order), len(want), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("cleanup %d = %q, want %q (%v)", i, order[i], want[i], order)
		}
	}
}

func TestRunCleanupsIsIdempotent(t *testing.T) {
	resetCleanups(t)

	calls := 0
	registerCleanup(func() { calls++ })

	// Commands keep their own `defer b.Close()` for the success path, so the
	// stack is routinely drained twice. The second drain must be free.
	runCleanups()
	runCleanups()

	if calls != 1 {
		t.Fatalf("cleanup ran %d times, want 1", calls)
	}
}

func TestRegisterCleanupDuringDrainIsNotLost(t *testing.T) {
	resetCleanups(t)

	// openPage registers the browser, then applyConfig* can exitErr; a cleanup
	// registered while another is running must still be drained.
	inner := false
	registerCleanup(func() {
		registerCleanup(func() { inner = true })
	})

	runCleanups()
	runCleanups()

	if !inner {
		t.Fatal("cleanup registered during a drain was dropped")
	}
}

func TestRegisterCleanupIsConcurrencySafe(t *testing.T) {
	resetCleanups(t)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			registerCleanup(func() {})
		}()
	}
	wg.Wait()

	cleanupMu.Lock()
	got := len(cleanups)
	cleanupMu.Unlock()

	if got != 50 {
		t.Fatalf("registered %d cleanups, want 50", got)
	}
	resetCleanups(t)
}
