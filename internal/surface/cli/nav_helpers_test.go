package cli

import "testing"

// TestConsumeNavChallengeRecoveredIsOneShot mirrors
// agentSession.consumeChallengeRecovered for the CLI's package-level flag:
// reading it must reset it, so a stale value can never be read twice within
// the same process.
func TestConsumeNavChallengeRecoveredIsOneShot(t *testing.T) {
	old := navChallengeRecovered
	defer func() { navChallengeRecovered = old }()

	navChallengeRecovered = true
	if got := consumeNavChallengeRecovered(); !got {
		t.Fatal("expected first consume to observe the recovered flag")
	}
	if navChallengeRecovered {
		t.Fatal("expected navChallengeRecovered to be reset to false after consumption")
	}
	if got := consumeNavChallengeRecovered(); got {
		t.Fatal("expected a second consume to observe false")
	}
}
