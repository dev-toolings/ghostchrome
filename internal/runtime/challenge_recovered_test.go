package runtime

import "testing"

// TestConsumeChallengeRecoveredIsOneShot verifies that reading the SSR
// opt-in flag resets it: only the "extract" immediately after a recovered
// "navigate" should see it as true.
func TestConsumeChallengeRecoveredIsOneShot(t *testing.T) {
	s := &Session{challengeRecovered: true}

	if got := s.consumeChallengeRecovered(); !got {
		t.Fatal("expected first consume to observe the recovered flag")
	}
	if s.challengeRecovered {
		t.Fatal("expected challengeRecovered to be reset to false after consumption")
	}
	if got := s.consumeChallengeRecovered(); got {
		t.Fatal("expected a second consume (no intervening navigate) to observe false")
	}
}

// TestResetsChallengeRecoveredOpList locks down exactly which JSONL ops
// change the current page and therefore must reset the one-shot SSR opt-in
// before the next "extract" runs. Ops that never navigate (hover, fill,
// scroll, eval, screenshot, wait, check/uncheck, errors, url) intentionally
// do NOT reset it.
func TestResetsChallengeRecoveredOpList(t *testing.T) {
	pageChanging := []string{"back", "forward", "reload", "click", "dblclick", "type", "press", "select"}
	for _, op := range pageChanging {
		if !resetsChallengeRecovered(op) {
			t.Errorf("expected %q to reset challengeRecovered", op)
		}
	}

	nonResetting := []string{"extract", "hover", "fill", "scroll_by", "scroll_to", "eval", "screenshot", "wait", "check", "uncheck", "errors", "url", "init", "navigate", "close", ""}
	for _, op := range nonResetting {
		if resetsChallengeRecovered(op) {
			t.Errorf("expected %q to NOT reset challengeRecovered", op)
		}
	}
}

// TestDispatchResetsChallengeRecoveredOnPageChangingOp exercises the actual
// Dispatch() entry point: a page-changing op must reset the flag even when
// the handler itself errors out early on invalid args (i.e. before ever
// touching a page), so this stays a network-free, browser-free unit test.
func TestDispatchResetsChallengeRecoveredOnPageChangingOp(t *testing.T) {
	s := &Session{challengeRecovered: true}

	// "click" validates its ref before calling ensurePage(); an empty ref
	// short-circuits with an error and never launches a browser.
	if _, err := s.Dispatch("click", nil); err == nil {
		t.Fatal("expected click with no ref to error out")
	}
	if s.challengeRecovered {
		t.Fatal("expected challengeRecovered to be reset by a page-changing op even on early error")
	}
}

// TestDispatchDoesNotResetChallengeRecoveredOnNonPageChangingOp mirrors the
// above for an op that must NOT touch the flag.
func TestDispatchDoesNotResetChallengeRecoveredOnNonPageChangingOp(t *testing.T) {
	s := &Session{challengeRecovered: true}

	if _, err := s.Dispatch("hover", nil); err == nil {
		t.Fatal("expected hover with no ref to error out")
	}
	if !s.challengeRecovered {
		t.Fatal("expected challengeRecovered to remain true after a non-page-changing op")
	}
}
